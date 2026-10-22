package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeGitHub serves the GitHub API for one release of virtbase/proxbase and its
// assets under /download/, and records the Authorization header per path.
type fakeGitHub struct {
	*httptest.Server
	tag    string
	assets map[string][]byte

	mu   sync.Mutex
	auth map[string]string
}

func newFakeGitHub(t *testing.T, tag string, assets map[string][]byte) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{tag: tag, assets: assets, auth: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.auth[r.URL.Path] = r.Header.Get("Authorization")
	f.mu.Unlock()
	switch p := r.URL.Path; {
	case p == "/repos/virtbase/proxbase/releases/latest", p == "/repos/virtbase/proxbase/releases/tags/"+f.tag:
		rel := Release{Tag: f.tag, URL: f.URL + "/release"}
		for name, data := range f.assets {
			rel.Assets = append(rel.Assets, Asset{Name: name, URL: f.URL + "/download/" + name, Size: int64(len(data))})
		}
		_ = json.NewEncoder(w).Encode(rel)
	case strings.HasPrefix(p, "/download/"):
		data, ok := f.assets[strings.TrimPrefix(p, "/download/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeGitHub) client() *Client {
	c := NewClient("proxbase-test")
	c.API = f.URL
	return c
}

func (f *fakeGitHub) authorization(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.auth[path]
}

// tarGz builds a release archive.
func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func checksums(files map[string][]byte) []byte {
	var b strings.Builder
	for name, data := range files {
		fmt.Fprintf(&b, "%s  %s\n", sha256Hex(data), name)
	}
	return []byte(b.String())
}

func TestClient_Release(t *testing.T) {
	t.Parallel()
	gh := newFakeGitHub(t, "v0.2.2", map[string][]byte{ChecksumsFile: []byte("")})
	tests := []struct {
		name    string
		version string
		wantErr error
	}{
		{name: "latest by default", version: ""},
		{name: "latest", version: "latest"},
		{name: "tag", version: "v0.2.2"},
		{name: "tag without v", version: "0.2.2"},
		{name: "missing tag", version: "v9.9.9", wantErr: ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rel, err := gh.client().Release(t.Context(), tt.version)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Release(%q) error = %v, want %v", tt.version, err, tt.wantErr)
			}
			if err == nil && rel.Tag != "v0.2.2" {
				t.Errorf("Release(%q).Tag = %q, want v0.2.2", tt.version, rel.Tag)
			}
		})
	}
	for _, version := range []string{"abc", "v0.2.2-7-gabc1234", "v0.2"} {
		t.Run("invalid "+version, func(t *testing.T) {
			t.Parallel()
			if _, err := gh.client().Release(t.Context(), version); err == nil || !strings.Contains(err.Error(), "invalid release version") {
				t.Errorf("Release(%q) error = %v, want invalid release version", version, err)
			}
		})
	}
}

func TestClient_TokenOnlyForAPI(t *testing.T) {
	t.Parallel()
	gh := newFakeGitHub(t, "v0.2.2", map[string][]byte{ChecksumsFile: []byte("")})
	c := gh.client()
	c.Token = "secret"
	rel, err := c.Release(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	// The assets are on the same host here; point the client at another API URL
	// so the download counts as a foreign host.
	c.API = strings.Replace(gh.URL, "127.0.0.1", "localhost", 1)
	if _, err := c.Checksums(t.Context(), rel, nil); err != nil {
		t.Fatal(err)
	}
	if got := gh.authorization("/repos/virtbase/proxbase/releases/latest"); got != "Bearer secret" {
		t.Errorf("API request Authorization = %q, want the token", got)
	}
	if got := gh.authorization("/download/" + ChecksumsFile); got != "" {
		t.Errorf("download Authorization = %q, want none", got)
	}
}

func TestClient_Errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "rate limit",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusForbidden)
			},
			want: "rate limit exceeded; set GITHUB_TOKEN",
		},
		{
			name:    "server error",
			handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
			want:    "502 Bad Gateway",
		},
		{
			name:    "not json",
			handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
			want:    "decode latest release",
		},
		{
			name: "draft",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"tag_name":"v1.0.0","draft":true}`))
			},
			want: ErrNotFound.Error(),
		},
		{
			name: "redirect to plain http",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "http://example.com/latest", http.StatusFound)
			},
			want: "not https",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(tt.handler)
			t.Cleanup(srv.Close)
			c := NewClient("proxbase-test")
			c.API = srv.URL
			if _, err := c.Release(t.Context(), ""); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Release error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestClient_Checksums(t *testing.T) {
	t.Parallel()
	archive := []byte("archive")
	sums := checksums(map[string][]byte{"proxbase_0.2.2_linux_amd64.tar.gz": archive})
	tests := []struct {
		name    string
		assets  map[string][]byte
		verify  Verifier
		want    string // expected error substring; "" for success
		wantErr error
	}{
		{name: "without signature", assets: map[string][]byte{ChecksumsFile: sums}},
		{
			name:   "signature verified",
			assets: map[string][]byte{ChecksumsFile: sums, BundleFile: []byte("bundle")},
			verify: func(_ context.Context, checksums, bundle []byte) error {
				if !bytes.Equal(checksums, sums) || string(bundle) != "bundle" {
					return errors.New("wrong input")
				}
				return nil
			},
		},
		{
			name:   "bad signature",
			assets: map[string][]byte{ChecksumsFile: sums, BundleFile: []byte("bundle")},
			verify: func(context.Context, []byte, []byte) error { return errors.New("no matching signature") },
			want:   "no matching signature",
		},
		{
			name:    "missing bundle",
			assets:  map[string][]byte{ChecksumsFile: sums},
			verify:  func(context.Context, []byte, []byte) error { return nil },
			wantErr: ErrMissingAsset,
		},
		{name: "missing checksums", assets: map[string][]byte{}, wantErr: ErrMissingAsset},
		{name: "malformed", assets: map[string][]byte{ChecksumsFile: []byte("deadbeef  proxbase.tar.gz\n")}, want: "malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gh := newFakeGitHub(t, "v0.2.2", tt.assets)
			c := gh.client()
			rel, err := c.Release(t.Context(), "")
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.Checksums(t.Context(), rel, tt.verify)
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Checksums error = %v, want %v", err, tt.wantErr)
				}
			case tt.want != "":
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("Checksums error = %v, want %q", err, tt.want)
				}
			case err != nil:
				t.Fatal(err)
			case got["proxbase_0.2.2_linux_amd64.tar.gz"] != sha256Hex(archive):
				t.Errorf("Checksums = %v, want the archive's sha256", got)
			}
		})
	}
}

func TestClient_Download(t *testing.T) {
	t.Parallel()
	archive := []byte("archive")
	name := ArchiveName("v0.2.2")
	tests := []struct {
		name    string
		assets  map[string][]byte
		sums    map[string]string
		wantErr error
	}{
		{name: "verified", assets: map[string][]byte{name: archive}, sums: map[string]string{name: sha256Hex(archive)}},
		{name: "checksum mismatch", assets: map[string][]byte{name: []byte("tampered")}, sums: map[string]string{name: sha256Hex(archive)}, wantErr: ErrChecksum},
		{name: "not in checksums", assets: map[string][]byte{name: archive}, sums: map[string]string{}, wantErr: ErrMissingAsset},
		{name: "missing asset", assets: map[string][]byte{}, sums: map[string]string{name: sha256Hex(archive)}, wantErr: ErrMissingAsset},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gh := newFakeGitHub(t, "v0.2.2", tt.assets)
			c := gh.client()
			rel, err := c.Release(t.Context(), "")
			if err != nil {
				t.Fatal(err)
			}
			data, err := c.Download(t.Context(), rel, name, tt.sums)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Download error = %v, want %v", err, tt.wantErr)
			}
			if err == nil && !bytes.Equal(data, archive) {
				t.Errorf("Download = %q, want %q", data, archive)
			}
		})
	}
	t.Run("too large", func(t *testing.T) {
		t.Parallel()
		rel := &Release{Tag: "v0.2.2", Assets: []Asset{{Name: name, URL: "https://example.invalid/x", Size: maxArchive + 1}}}
		_, err := NewClient("proxbase-test").Download(t.Context(), rel, name, map[string]string{name: sha256Hex(archive)})
		if err == nil || !strings.Contains(err.Error(), "limit") {
			t.Errorf("Download error = %v, want a size limit error", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		gh := newFakeGitHub(t, "v0.2.2", map[string][]byte{name: archive})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		rel := &Release{Tag: "v0.2.2", Assets: []Asset{{Name: name, URL: gh.URL + "/download/" + name}}}
		if _, err := gh.client().Download(ctx, rel, name, map[string]string{name: sha256Hex(archive)}); !errors.Is(err, context.Canceled) {
			t.Errorf("Download error = %v, want context.Canceled", err)
		}
	})
}

func TestCheckURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		url    string
		wantOK bool
	}{
		{url: "https://github.com/virtbase/proxbase/releases/download/v0.2.2/checksums.txt", wantOK: true},
		{url: "http://127.0.0.1:8080/x", wantOK: true},
		{url: "http://[::1]:8080/x", wantOK: true},
		{url: "http://localhost/x", wantOK: true},
		{url: "http://github.com/x", wantOK: false},
		{url: "http://127.0.0.1.example.com/x", wantOK: false},
		{url: "ftp://127.0.0.1/x", wantOK: false},
		{url: "file:///etc/passwd", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkURL(u); (err == nil) != tt.wantOK {
				t.Errorf("checkURL(%s) = %v, want ok %v", tt.url, err, tt.wantOK)
			}
		})
	}
}
