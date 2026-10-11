// Package aptcache is a caching HTTP proxy for the nodes' apt. Nodes of a
// cluster download the same packages (Ceph, upgrades), and every create downloads
// them again; through the proxy each package is fetched from the mirror once and
// then served from the host's cache.
//
// Only .deb files are cached: their names are versioned, so the content never
// changes, and apt checks their hashes against the signed repository index. All
// other requests (indexes, Release files) go to the mirror unchanged. Requests
// are limited to the Debian and Proxmox repository hosts.
//
// A package that apt rejects (Hash Sum mismatch: a mirror served a broken file)
// is fetched again when apt asks with Cache-Control: no-cache
// (-o Acquire::http::No-Cache=true), which proxbase's apt runs do on a retry.
package aptcache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
)

// Hosts are the repository hosts the proxy serves.
var Hosts = []string{"download.proxmox.com", "deb.debian.org", "security.debian.org"}

// maxAge is how long a cached package is kept after it was last used.
const maxAge = 30 * 24 * time.Hour

// hostAddr is the host as seen from a guest on QEMU user networking.
const hostAddr = "10.0.2.2"

// Proxy is a running apt cache.
type Proxy struct {
	root     *os.Root // the cache directory; no file access can leave it
	srv      *http.Server
	client   *http.Client
	inflight singleflight.Group
	// URL is the proxy's address as seen from the nodes (for http_proxy).
	URL string
	// Addr is the address the proxy listens on, on the host.
	Addr string
}

// Start serves the cache in dir on a free port of the host's loopback address,
// which nodes on QEMU user networking reach as 10.0.2.2. It first removes
// packages unused for 30 days.
func Start(dir string) (*Proxy, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("apt cache: %w", err)
	}
	prune(root, time.Now().Add(-maxAge))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("apt cache: %w", err)
	}
	p := &Proxy{
		root:   root,
		client: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, IdleConnTimeout: 30 * time.Second}},
		URL:    "http://" + net.JoinHostPort(hostAddr, strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)),
		Addr:   ln.Addr().String(),
	}
	p.srv = &http.Server{Handler: p, ReadHeaderTimeout: 30 * time.Second}
	// Serve returns when Close is called; apt reports any other failure itself.
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

// Close stops the proxy.
func (p *Proxy) Close() error { return errors.Join(p.srv.Close(), p.root.Close()) }

// ServeHTTP handles proxy requests ("GET http://host/path").
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "only GET and HEAD", http.StatusMethodNotAllowed)
		return
	}
	u := r.URL
	if u.Scheme != "http" || !slices.Contains(Hosts, u.Hostname()) || (u.Port() != "" && u.Port() != "80") {
		http.Error(w, "not a repository host", http.StatusForbidden)
		return
	}
	clean := path.Clean("/" + u.Path)
	if r.Method == http.MethodGet && strings.HasSuffix(clean, ".deb") && u.RawQuery == "" {
		p.serveDeb(w, r, u.Hostname(), clean)
		return
	}
	p.pass(w, r)
}

// pass forwards a request to the mirror and streams the response back.
func (p *Proxy) pass(w http.ResponseWriter, r *http.Request) {
	// ServeHTTP admits only plain HTTP to the repository hosts.
	req, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), nil) //nolint:gosec // allow-listed hosts only
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for _, h := range []string{"If-Modified-Since", "If-None-Match", "Range", "If-Range", "Cache-Control", "User-Agent"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	resp, err := p.client.Do(req) //nolint:gosec // allow-listed hosts only
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Last-Modified", "ETag", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// serveDeb serves a package from the cache, downloading it first if needed.
func (p *Proxy) serveDeb(w http.ResponseWriter, r *http.Request, host, clean string) {
	// clean is rooted and cleaned, so the name stays below host/.
	name := filepath.Join(host, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	_, err := p.root.Stat(name)
	if err != nil || noCache(r) {
		// Concurrent requests for the same package share one download. It must
		// not end when the first requester goes away.
		ctx := context.WithoutCancel(r.Context())
		_, err, _ := p.inflight.Do(name, func() (any, error) {
			return nil, p.download(ctx, "http://"+host+clean, name)
		})
		if err != nil {
			var se statusError
			if errors.As(err, &se) {
				http.Error(w, err.Error(), int(se))
				return
			}
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
	}
	f, err := p.root.Open(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	_ = p.root.Chtimes(name, now, now) // last use, for pruning
	w.Header().Set("Content-Type", "application/vnd.debian.binary-package")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// statusError is a non-200 status from the mirror.
type statusError int

func (e statusError) Error() string { return "mirror answered " + http.StatusText(int(e)) }

// download fetches url into the cache as name, atomically and only if complete.
func (p *Proxy) download(ctx context.Context, url, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // allow-listed hosts only
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req) //nolint:gosec // allow-listed hosts only
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError(resp.StatusCode)
	}
	if err := p.root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(name), fmt.Sprintf(".part-%d", rand.Uint64())) //nolint:gosec // a unique name, not a secret
	f, err := p.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = p.root.Remove(tmp) }() // gone after the rename
	n, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return fmt.Errorf("download %s: got %d of %d bytes", url, n, resp.ContentLength)
	}
	if err := checkDeb(p.root, tmp); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	return p.root.Rename(tmp, name)
}

// noCache reports whether the client asked to bypass caches.
func noCache(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Cache-Control"), "no-cache") || r.Header.Get("Pragma") == "no-cache"
}

// debMagic starts every .deb file (an ar archive).
const debMagic = "!<arch>\n"

// checkDeb rejects files that are not .deb packages, such as error pages.
func checkDeb(root *os.Root, name string) error {
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, len(debMagic))
	if _, err := io.ReadFull(f, head); err != nil || string(head) != debMagic {
		return errors.New("not a Debian package")
	}
	return nil
}

// prune removes cached packages last used before cutoff, and leftovers of
// interrupted downloads.
func prune(root *os.Root, cutoff time.Time) {
	_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err == nil && (info.ModTime().Before(cutoff) || strings.HasPrefix(d.Name(), ".part-")) {
			_ = root.Remove(p)
		}
		return nil
	})
}
