package guestimage

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/virtbase/proxbase/internal/config"
)

const index = `Package: debian-13-standard
Version: 13.4-1
Type: lxc
Architecture: amd64
Location: system/debian-13-standard_13.4-1_amd64.tar.zst
sha512sum: aa
Description: Debian 13
 A small Debian system.

Package: debian-13-standard
Version: 13.10-2
Type: lxc
Architecture: amd64
Location: system/debian-13-standard_13.10-2_amd64.tar.zst
sha512sum: bb

Package: debian-13-standard
Version: 13.11-1
Type: lxc
Architecture: arm64
Location: system/debian-13-standard_13.11-1_arm64.tar.zst

Package: turnkey-wordpress
Version: 18.0
Type: turnkey
Architecture: amd64
Location: turnkey/x.tar.gz
`

func TestParseIndexNewest(t *testing.T) {
	entries, err := ParseIndex(strings.NewReader(index))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries %+v, want the two amd64 lxc templates", entries)
	}
	c, ok := Newest(entries, "debian-13-standard")
	if !ok || c.Version != "13.10-2" || c.File() != "debian-13-standard_13.10-2_amd64.tar.zst" || c.SHA512 != "bb" {
		t.Errorf("newest %+v", c)
	}
	if _, ok := Newest(entries, "alpine-3"); ok {
		t.Error("found a template that is not in the index")
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want int
	}{{"13.10-2", "13.4-1", 1}, {"13.4-1", "13.4-1", 0}, {"13.4", "13.4-1", -1}, {"9.0-1", "13.0-1", -1}} {
		if got := compareVersions(tt.a, tt.b); (got > 0) != (tt.want > 0) || (got < 0) != (tt.want < 0) {
			t.Errorf("compareVersions(%s, %s) = %d, want sign of %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestCloudImageByURL(t *testing.T) {
	data := append([]byte("QFI\xfb"), make([]byte, 1000)...)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	sum := sha256.Sum256(data)
	tmpl := config.VMTemplate{Name: "img", Image: srv.URL + "/disk.qcow2", Checksum: "sha256:" + hex.EncodeToString(sum[:])}
	cache := t.TempDir()
	img, err := CloudImage(context.Background(), tmpl, cache, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if img.Size != int64(len(data)) || !strings.HasSuffix(img.Name, ".qcow2") || !strings.HasPrefix(img.Name, "proxbase-sha256-") {
		t.Errorf("image %+v", img)
	}
	if _, err := CloudImage(context.Background(), tmpl, cache, t.Logf); err != nil || requests.Load() != 1 {
		t.Errorf("second call downloaded again (%d requests, %v)", requests.Load(), err)
	}
	tmpl.Checksum = "sha256:" + strings.Repeat("0", 64)
	if _, err := CloudImage(context.Background(), tmpl, cache, t.Logf); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("wrong checksum: %v", err)
	}
}

func TestContainerImage(t *testing.T) {
	data := []byte("container template")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/system/ct.tar.zst" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	old := ContainerMirror
	ContainerMirror = srv.URL + "/"
	defer func() { ContainerMirror = old }()
	sum := sha512.Sum512(data)
	ct := Container{Name: "ct", Version: "1.0-1", Location: "system/ct.tar.zst", SHA512: hex.EncodeToString(sum[:])}
	cache := t.TempDir()
	img, err := ContainerImage(context.Background(), ct, cache, t.Logf)
	if err != nil || img.Name != "ct.tar.zst" || img.Size != int64(len(data)) {
		t.Fatalf("image %+v, %v", img, err)
	}
	if err := SaveContainer(cache, ct); err != nil {
		t.Fatal(err)
	}
	if got, ok := LastContainer(cache, "ct"); !ok || got != ct {
		t.Errorf("LastContainer = %+v, %v", got, ok)
	}
	ct.SHA512 = "bad"
	if _, err := ContainerImage(context.Background(), ct, t.TempDir(), t.Logf); err == nil {
		t.Error("accepted an entry without a valid SHA-512")
	}
}

// Without network access a known image falls back to its last verified download.
func TestCloudImageOffline(t *testing.T) {
	const alias = "test-offline"
	config.CloudImages[alias] = config.CloudImage{URL: "http://127.0.0.1:1/disk.qcow2", SumsURL: "http://127.0.0.1:1/SHA256SUMS", Algorithm: "sha256"}
	defer delete(config.CloudImages, alias)
	cache := t.TempDir()
	tmpl := config.VMTemplate{Name: "img", Image: alias}
	if _, err := CloudImage(context.Background(), tmpl, cache, t.Logf); err == nil {
		t.Fatal("offline without a previous download succeeded")
	}

	data := []byte("raw disk")
	sum := sha256.Sum256(data)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + " *disk.qcow2\n"))
			return
		}
		_, _ = w.Write(data)
	}))
	config.CloudImages[alias] = config.CloudImage{URL: srv.URL + "/disk.qcow2", SumsURL: srv.URL + "/SHA256SUMS", Algorithm: "sha256"}
	online, err := CloudImage(context.Background(), tmpl, cache, t.Logf)
	srv.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(online.Name, ".raw") {
		t.Errorf("raw image named %s", online.Name)
	}
	offline, err := CloudImage(context.Background(), tmpl, cache, t.Logf)
	if err != nil || offline.Path != online.Path {
		t.Errorf("offline: %+v, %v; want the cached %s", offline, err, online.Path)
	}
}
