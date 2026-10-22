// Package selfupdate upgrades the proxbase binary to a GitHub release: it finds the
// release, downloads the archive for this platform, verifies it against the
// release's checksums.txt (and the Sigstore signature of that file, with cosign)
// and renames the new binary over the running one. It also tells installs that a
// package manager owns (Homebrew, deb/rpm/apk, go install, the container image)
// apart, because those must be upgraded by their package manager.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

const (
	// Repo is the GitHub repository with the releases.
	Repo = "virtbase/proxbase"
	// API is the GitHub REST API.
	API = "https://api.github.com"

	// ChecksumsFile lists the SHA-256 of every release asset.
	ChecksumsFile = "checksums.txt"
	// BundleFile is the Sigstore bundle with the signature of ChecksumsFile.
	BundleFile = ChecksumsFile + ".sigstore.json"

	maxJSON    = 4 << 20   // release JSON
	maxSmall   = 1 << 20   // checksums.txt and its bundle
	maxArchive = 128 << 20 // a release archive is about 5 MiB

	apiTimeout      = 30 * time.Second
	downloadTimeout = 5 * time.Minute
)

var (
	// ErrNotFound means the release does not exist.
	ErrNotFound = errors.New("not found")
	// ErrMissingAsset means the release lacks a file, e.g. for an unsupported platform.
	ErrMissingAsset = errors.New("missing release asset")
	// ErrChecksum means a download does not match checksums.txt.
	ErrChecksum = errors.New("checksum mismatch")
)

// Release is a GitHub release.
type Release struct {
	Tag    string  `json:"tag_name"`
	URL    string  `json:"html_url"`
	Draft  bool    `json:"draft"`
	Assets []Asset `json:"assets"`
}

// Asset is a file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Asset returns the file called name.
func (r *Release) Asset(name string) (Asset, error) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("release %s has no %s: %w", r.Tag, name, ErrMissingAsset)
}

// ArchiveName is the name of the release archive for this platform, as GoReleaser
// names it: proxbase_0.2.2_linux_amd64.tar.gz.
func ArchiveName(tag string) string { return assetName(tag, "tar.gz") }

// assetName names an archive or package of the release tag for this platform.
func assetName(tag, ext string) string {
	return fmt.Sprintf("proxbase_%s_%s_%s.%s", strings.TrimPrefix(tag, "v"), runtime.GOOS, runtime.GOARCH, ext)
}

// Verifier checks the signature of checksums.txt against its Sigstore bundle.
type Verifier func(ctx context.Context, checksums, bundle []byte) error

// Client finds and downloads releases.
type Client struct {
	API       string // GitHub API base URL
	Repo      string // owner/name
	Token     string // optional GitHub token for the API rate limit; sent to API only
	UserAgent string
	HTTP      *http.Client
}

// NewClient returns a client for the proxbase releases on github.com.
func NewClient(userAgent string) *Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSHandshakeTimeout = 10 * time.Second
	t.ResponseHeaderTimeout = 30 * time.Second
	return &Client{
		API:       API,
		Repo:      Repo,
		UserAgent: userAgent,
		HTTP: &http.Client{
			Transport: t,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("too many redirects")
				}
				return checkURL(req.URL)
			},
		},
	}
}

// Release returns the release with the tag version (v0.2.2 or 0.2.2), or the
// latest release for "" or "latest". Drafts and, for latest, pre-releases are
// never returned.
func (c *Client) Release(ctx context.Context, version string) (*Release, error) {
	path, what := "releases/latest", "latest release"
	if version != "" && version != "latest" {
		v, err := ParseVersion(version)
		if err != nil || !v.IsRelease() {
			return nil, fmt.Errorf("invalid release version %q, want e.g. v0.2.2", version)
		}
		path, what = "releases/tags/"+url.PathEscape(v.Tag()), "release "+v.Tag()
	}
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	data, err := c.get(ctx, strings.TrimRight(c.API, "/")+"/repos/"+c.Repo+"/"+path, maxJSON)
	if errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("%s of %s: %w", what, c.Repo, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var r Release
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decode %s: %w", what, err)
	}
	if r.Tag == "" || r.Draft {
		return nil, fmt.Errorf("%s of %s: %w", what, c.Repo, ErrNotFound)
	}
	return &r, nil
}

// Checksums downloads checksums.txt of rel and returns file name -> SHA-256 (hex).
// With a verify function, it first checks the signature of the file.
func (c *Client) Checksums(ctx context.Context, rel *Release, verify Verifier) (map[string]string, error) {
	a, err := rel.Asset(ChecksumsFile)
	if err != nil {
		return nil, err
	}
	data, err := c.download(ctx, a, maxSmall)
	if err != nil {
		return nil, err
	}
	if verify != nil {
		b, err := rel.Asset(BundleFile)
		if err != nil {
			return nil, err
		}
		bundle, err := c.download(ctx, b, maxSmall)
		if err != nil {
			return nil, err
		}
		if err := verify(ctx, data, bundle); err != nil {
			return nil, fmt.Errorf("signature of %s %s: %w", rel.Tag, ChecksumsFile, err)
		}
	}
	return parseChecksums(data)
}

// parseChecksums parses `sha256sum` output, rejecting anything else.
func parseChecksums(data []byte) (map[string]string, error) {
	sums := map[string]string{}
	for line := range strings.Lines(string(data)) {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 || len(f[0]) != 2*sha256.Size {
			return nil, fmt.Errorf("malformed %s line %q", ChecksumsFile, strings.TrimSpace(line))
		}
		if _, err := hex.DecodeString(f[0]); err != nil {
			return nil, fmt.Errorf("malformed %s line %q", ChecksumsFile, strings.TrimSpace(line))
		}
		sums[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
	}
	return sums, nil
}

// Download downloads the file name of rel and checks it against sums.
func (c *Client) Download(ctx context.Context, rel *Release, name string, sums map[string]string) ([]byte, error) {
	want, ok := sums[name]
	if !ok {
		return nil, fmt.Errorf("%s of %s lists no %s: %w", ChecksumsFile, rel.Tag, name, ErrMissingAsset)
	}
	a, err := rel.Asset(name)
	if err != nil {
		return nil, err
	}
	data, err := c.download(ctx, a, maxArchive)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("%s: %w: got sha256 %s, %s has %s", name, ErrChecksum, got, ChecksumsFile, want)
	}
	return data, nil
}

func (c *Client) download(ctx context.Context, a Asset, limit int64) ([]byte, error) {
	if a.Size > limit {
		return nil, fmt.Errorf("%s is %d bytes, more than the limit of %d", a.Name, a.Size, limit)
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	return c.get(ctx, a.URL, limit)
}

// get fetches rawURL into memory, at most limit bytes. Only requests to the API
// carry the token.
func (c *Client) get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if err := checkURL(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	if api, err := url.Parse(c.API); err == nil && u.Scheme == api.Scheme && u.Host == api.Host {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("GET %s: %w", u.Redacted(), ErrNotFound)
	case (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) &&
		resp.Header.Get("X-RateLimit-Remaining") == "0":
		return nil, fmt.Errorf("GET %s: GitHub API rate limit exceeded; set GITHUB_TOKEN", u.Redacted())
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GET %s: %s", u.Redacted(), resp.Status)
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("GET %s: %d bytes, more than the limit of %d", u.Redacted(), resp.ContentLength, limit)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", u.Redacted(), err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GET %s: more than the limit of %d bytes", u.Redacted(), limit)
	}
	return data, nil
}

// checkURL allows HTTPS, and plain HTTP only to loopback addresses (test servers).
func checkURL(u *url.URL) error {
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return fmt.Errorf("refusing to download from %s: not https", u.Redacted())
}
