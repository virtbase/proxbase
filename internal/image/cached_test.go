package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCached(t *testing.T) {
	cache := t.TempDir()
	dir := filepath.Join(cache, "iso")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := strings.Repeat("a", 64)
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if Cached("9.2", cache) || Cached("9.2-1", cache) {
		t.Fatal("empty cache reports an ISO")
	}
	write("SHA256SUMS", sum+"  proxmox-ve_9.2-1.iso\n"+sum+"  proxmox-ve_9.1-1.iso\n")
	write("proxmox-ve_9.2-1.iso", "iso")
	if Cached("9.2", cache) {
		t.Error("unverified ISO counts as cached")
	}
	write("proxmox-ve_9.2-1.iso.sha256", sum+" 3\n")
	for version, want := range map[string]bool{"9.2": true, "9.2-1": true, "9.1": false, "9.1-1": false, "8.4": false} {
		if got := Cached(version, cache); got != want {
			t.Errorf("Cached(%q) = %v, want %v", version, got, want)
		}
	}
}
