package firstboot

import (
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

// sh parses (and with run, executes) a script with the guest's /bin/sh.
func sh(t *testing.T, script string, run bool) string {
	t.Helper()
	args := []string{"-n"}
	if run {
		args = []string{"-eu"}
	}
	cmd := exec.CommandContext(t.Context(), "sh", args...)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sh %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestHook(t *testing.T) {
	t.Parallel()
	h := Hook()
	if strings.Contains(h, "@") {
		t.Fatalf("unreplaced placeholder in hook:\n%s", h)
	}
	for _, want := range []string{"GRUB_TIMEOUT=0", ">" + IdentityMarker, "pending-first-boot-setup", "/var/log/pveam.log"} {
		if !strings.Contains(h, want) {
			t.Errorf("hook lacks %q", want)
		}
	}
	sh(t, h, false)
}

func TestServe(t *testing.T) {
	t.Parallel()
	s, err := Serve()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !strings.HasPrefix(s.URL, "http://10.0.2.2:") {
		t.Fatalf("URL %s is not the host as seen from the guest", s.URL)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, strings.Replace(s.URL, "10.0.2.2", "127.0.0.1", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != Hook() {
		t.Fatalf("GET %s: %s, %d bytes", s.URL, resp.Status, len(body))
	}
}

// The identity file is sourced by the hook; values must arrive unchanged.
func TestIdentity(t *testing.T) {
	t.Parallel()
	keys := []string{"ssh-ed25519 AAAA proxbase@lab\n", "ssh-rsa BBBB it's mine"}
	id := Identity("pve2", "lab.internal", `p'w"$x`, keys)
	got := sh(t, string(id)+`printf '%s|%s|%s|%s' "$NODE" "$DOMAIN" "$PASSWORD" "$KEYS"`, true)
	want := "pve2|lab.internal|p'w\"$x|ssh-ed25519 AAAA proxbase@lab\nssh-rsa BBBB it's mine"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBake(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, ceph string
		want, not  []string
	}{
		{name: "plain", want: []string{"GRUB_TIMEOUT=0", "pveam.log"}, not: []string{"apt-get"}},
		{name: "ceph", ceph: "squid", want: []string{"debian/ceph-squid", "ceph-mds", "policy-rc.d", `"52:54:00:50:00:00"`, "ip route add default via 10.0.2.2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := Bake(tt.ceph, "52:54:00:50:00:00")
			for _, w := range tt.want {
				if !strings.Contains(b, w) {
					t.Errorf("bake lacks %q", w)
				}
			}
			for _, n := range tt.not {
				if strings.Contains(b, n) {
					t.Errorf("bake must not contain %q", n)
				}
			}
			sh(t, b, false)
		})
	}
}
