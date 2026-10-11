package aptcache

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mirror is a fake repository that counts requests per path.
type mirror struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits map[string]int
	gate chan struct{} // if set, .deb responses wait for it
}

func newMirror(t *testing.T) *mirror {
	t.Helper()
	m := &mirror{hits: map[string]int{}}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.hits[r.URL.Path]++
		gate := m.gate
		m.mu.Unlock()
		switch r.URL.Path {
		case "/missing.deb":
			http.NotFound(w, r)
			return
		case "/html.deb":
			_, _ = io.WriteString(w, "<html>captive portal</html>")
			return
		}
		if gate != nil {
			<-gate
		}
		body := "content of " + r.URL.Path
		if strings.HasSuffix(r.URL.Path, ".deb") {
			body = debMagic + body
		}
		_, _ = io.WriteString(w, body) //nolint:gosec // test server, plain text
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mirror) count(p string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hits[p]
}

// start runs a proxy whose upstream connections all go to the fake mirror.
func start(t *testing.T, m *mirror) (*Proxy, *http.Client) {
	t.Helper()
	p, err := Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	mirrorAddr := m.srv.Listener.Addr().String()
	p.client = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, mirrorAddr)
		},
	}}
	proxyURL, err := url.Parse("http://" + p.Addr)
	if err != nil {
		t.Fatal(err)
	}
	return p, &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 10 * time.Second}
}

func fetch(ctx context.Context, c *http.Client, u string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), err
}

func get(t *testing.T, c *http.Client, u string) (int, string) {
	t.Helper()
	code, body, err := fetch(t.Context(), c, u)
	if err != nil {
		t.Fatal(err)
	}
	return code, body
}

func TestProxy_CachesPackages(t *testing.T) {
	t.Parallel()
	m := newMirror(t)
	_, c := start(t, m)
	const deb = "http://deb.debian.org/debian/pool/main/c/ceph/ceph_19.2.3_amd64.deb"
	for range 2 {
		code, body := get(t, c, deb)
		if code != http.StatusOK || body != debMagic+"content of /debian/pool/main/c/ceph/ceph_19.2.3_amd64.deb" {
			t.Fatalf("got %d %q", code, body)
		}
	}
	if n := m.count("/debian/pool/main/c/ceph/ceph_19.2.3_amd64.deb"); n != 1 {
		t.Fatalf("mirror asked %d times, want 1", n)
	}
}

// apt asks with no-cache after a Hash Sum mismatch; the package is fetched again.
func TestProxy_NoCacheRefetches(t *testing.T) {
	t.Parallel()
	m := newMirror(t)
	_, c := start(t, m)
	const deb = "http://deb.debian.org/debian/pool/main/libn/libnvme/libnvme1t64_1.13-2_amd64.deb"
	get(t, c, deb)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, deb, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if n := m.count("/debian/pool/main/libn/libnvme/libnvme1t64_1.13-2_amd64.deb"); n != 2 {
		t.Fatalf("mirror asked %d times, want 2", n)
	}
}

func TestProxy_PassesIndexes(t *testing.T) {
	t.Parallel()
	m := newMirror(t)
	_, c := start(t, m)
	for range 2 {
		if code, _ := get(t, c, "http://download.proxmox.com/debian/pve/dists/trixie/InRelease"); code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
	}
	if n := m.count("/debian/pve/dists/trixie/InRelease"); n != 2 {
		t.Fatalf("indexes must not be cached: mirror asked %d times, want 2", n)
	}
}

func TestProxy_SharesConcurrentDownloads(t *testing.T) {
	t.Parallel()
	m := newMirror(t)
	m.gate = make(chan struct{})
	_, c := start(t, m)
	const deb = "http://download.proxmox.com/debian/ceph-squid/pool/x.deb"
	var wg sync.WaitGroup
	var ok atomic.Int32
	for range 5 {
		wg.Go(func() {
			if code, _, err := fetch(t.Context(), c, deb); err == nil && code == http.StatusOK {
				ok.Add(1)
			}
		})
	}
	// Let the requests arrive, then release the single upstream download.
	for m.count("/debian/ceph-squid/pool/x.deb") == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	close(m.gate)
	wg.Wait()
	if ok.Load() != 5 {
		t.Fatalf("%d of 5 requests succeeded", ok.Load())
	}
	if n := m.count("/debian/ceph-squid/pool/x.deb"); n != 1 {
		t.Fatalf("mirror asked %d times, want 1", n)
	}
}

func TestProxy_Rejects(t *testing.T) {
	t.Parallel()
	m := newMirror(t)
	_, c := start(t, m)
	tests := []struct {
		name string
		url  string
		want int
	}{
		{name: "other host", url: "http://example.com/x.deb", want: http.StatusForbidden},
		{name: "other port", url: "http://deb.debian.org:8080/x.deb", want: http.StatusForbidden},
		{name: "missing package", url: "http://deb.debian.org/missing.deb", want: http.StatusNotFound},
		{name: "not a package", url: "http://deb.debian.org/html.deb", want: http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _ := get(t, c, tt.url); code != tt.want {
				t.Errorf("status %d, want %d", code, tt.want)
			}
		})
	}
}

func TestPrune(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	old, recent, part := filepath.Join(dir, "a", "old.deb"), filepath.Join(dir, "a", "new.deb"), filepath.Join(dir, "a", ".part-1")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{old, recent, part} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * maxAge)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	prune(root, time.Now().Add(-maxAge))
	for p, want := range map[string]bool{old: false, recent: true, part: false} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", filepath.Base(p), err == nil, want)
		}
	}
}
