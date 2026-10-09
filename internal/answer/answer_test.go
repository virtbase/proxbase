package answer

import (
	"os"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	got := Render(Params{
		FQDN: "pve1.lab.internal", Mailto: "root@lab.internal", Keyboard: "de", Country: "de",
		Timezone: "Europe/Berlin", RootPassword: `pa"ss\`, SSHKeys: []string{"ssh-ed25519 AAAA proxbase\n"},
		MAC: "52:54:00:00:01:00", Filesystem: "zfs",
	})
	want, err := os.ReadFile("testdata/answer.toml")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderExt4(t *testing.T) {
	got := Render(Params{Filesystem: "ext4"})
	for _, bad := range []string{"zfs.raid", "btrfs.raid"} {
		if strings.Contains(got, bad) {
			t.Errorf("ext4 answer must not contain %s", bad)
		}
	}
}

func TestPassword(t *testing.T) {
	if a, b := Password(20), Password(20); len(a) != 20 || a == b {
		t.Fatalf("bad passwords %q %q", a, b)
	}
}
