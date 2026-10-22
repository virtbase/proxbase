// Package guestimage downloads and caches the images of guest templates on the
// host, so that each is downloaded once: cloud images for VM templates and
// container templates. Every file is verified against a checksum before use.
package guestimage

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/virtbase/proxbase/internal/config"
)

// Logf reports progress.
type Logf func(format string, a ...any)

const (
	// maxImage bounds a download.
	maxImage = 16 << 30
	// sumsTimeout bounds fetching a checksum file.
	sumsTimeout = 30 * time.Second
)

// ContainerMirror serves the container templates listed in the appliance
// index. The nodes verify the index's signature, which covers the SHA-512 of
// every template, so plain HTTP is enough.
var ContainerMirror = "http://download.proxmox.com/images/"

// Image is a cached image.
type Image struct {
	Path string // in the cache
	Name string // a file name for the node that keeps the image's format and checksum apart
	Size int64
}

// CloudImage returns the cached image of a VM template, downloading it if
// needed. Known images are checked against the checksum file next to them;
// without network access the last verified download is used.
func CloudImage(ctx context.Context, t config.VMTemplate, cacheDir string, logf Logf) (*Image, error) {
	dir := filepath.Join(cacheDir, "cloud")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	src, algorithm, digest, err := resolveCloud(ctx, t, dir)
	if err != nil {
		return nil, err
	}
	file := filepath.Join(dir, fmt.Sprintf("%s-%x-%s", algorithm, digest[:8], path.Base(src)))
	if !verified(file, digest) {
		logf("downloading %s", src)
		if err := download(ctx, src, file, algorithm, digest); err != nil {
			return nil, err
		}
	}
	if known, ok := config.CloudImages[t.Image]; ok {
		_ = saveLast(dir, t.Image, lastImage{URL: known.URL, Algorithm: algorithm, Digest: hex.EncodeToString(digest)})
	}
	ext, err := diskFormat(file)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	return &Image{Path: file, Name: fmt.Sprintf("proxbase-%s-%x.%s", algorithm, digest[:8], ext), Size: fi.Size()}, nil
}

// lastImage is the last verified download of a known image, for offline use.
type lastImage struct {
	URL       string `json:"url"`
	Algorithm string `json:"algorithm"`
	Digest    string `json:"digest"`
}

func saveLast(dir, name string, l lastImage) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+".json"), b, 0o644)
}

// resolveCloud returns the URL and checksum of a template's image.
func resolveCloud(ctx context.Context, t config.VMTemplate, dir string) (string, string, []byte, error) {
	known, ok := config.CloudImages[t.Image]
	if !ok {
		algorithm, digest, err := config.ParseChecksum(t.Checksum)
		return t.Image, algorithm, digest, err
	}
	if t.Checksum != "" {
		algorithm, digest, err := config.ParseChecksum(t.Checksum)
		return known.URL, algorithm, digest, err
	}
	sums, err := fetchSums(ctx, known.SumsURL)
	if err == nil {
		h, ok := sums[path.Base(known.URL)]
		if !ok {
			return "", "", nil, fmt.Errorf("%s does not list %s", known.SumsURL, path.Base(known.URL))
		}
		digest, err := hex.DecodeString(h)
		return known.URL, known.Algorithm, digest, err
	}
	// Offline: the last image verified for this name.
	b, rerr := os.ReadFile(filepath.Join(dir, t.Image+".json"))
	if rerr != nil {
		return "", "", nil, fmt.Errorf("checksums of %s: %w", t.Image, err)
	}
	var l lastImage
	if rerr := json.Unmarshal(b, &l); rerr != nil {
		return "", "", nil, fmt.Errorf("checksums of %s: %w", t.Image, err)
	}
	digest, derr := hex.DecodeString(l.Digest)
	return l.URL, l.Algorithm, digest, derr
}

// fetchSums reads a checksum file in sha256sum format: file name to hex digest.
func fetchSums(ctx context.Context, url string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, sumsTimeout)
	defer cancel()
	resp, err := get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	sums := map[string]string{}
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 {
			sums[strings.TrimPrefix(f[1], "*")] = f[0]
		}
	}
	return sums, sc.Err()
}

// Container is a container template of the appliance index (aplinfo).
type Container struct {
	Name     string // e.g. debian-13-standard
	Version  string // e.g. 13.6-1
	Location string // path below the mirror, e.g. system/debian-13-standard_13.6-1_amd64.tar.zst
	SHA512   string
}

// File is the template's file name on the nodes.
func (c Container) File() string { return path.Base(c.Location) }

// ParseIndex reads the amd64 container templates of an appliance index.
func ParseIndex(r io.Reader) ([]Container, error) {
	var out []Container
	sc := bufio.NewScanner(r)
	fields := map[string]string{}
	flush := func() {
		if fields["Type"] == "lxc" && fields["Architecture"] == "amd64" && fields["Location"] != "" {
			out = append(out, Container{Name: fields["Package"], Version: fields["Version"], Location: fields["Location"], SHA512: fields["sha512sum"]})
		}
		fields = map[string]string{}
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if k, v, ok := strings.Cut(line, ": "); ok && !strings.HasPrefix(line, " ") {
			fields[k] = v
		}
	}
	flush()
	return out, sc.Err()
}

// Newest returns the newest template called name.
func Newest(index []Container, name string) (Container, bool) {
	var best Container
	found := false
	for _, c := range index {
		if c.Name == name && (!found || compareVersions(c.Version, best.Version) > 0) {
			best, found = c, true
		}
	}
	return best, found
}

// compareVersions compares versions like 13.6-1 number by number.
func compareVersions(a, b string) int {
	split := func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '-' })
	}
	pa, pb := split(a), split(b)
	for i := range max(len(pa), len(pb)) {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x - y
		}
	}
	return 0
}

// ContainerImage returns the cached file of a container template, downloading it if needed.
func ContainerImage(ctx context.Context, c Container, cacheDir string, logf Logf) (*Image, error) {
	digest, err := hex.DecodeString(c.SHA512)
	if err != nil || len(digest) != sha512.Size {
		return nil, fmt.Errorf("container template %s: no valid SHA-512 in the index", c.Name)
	}
	dir := filepath.Join(cacheDir, "containers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, c.File())
	if !verified(file, digest) {
		logf("downloading %s", c.File())
		if err := download(ctx, ContainerMirror+c.Location, file, "sha512", digest); err != nil {
			return nil, err
		}
	}
	fi, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	return &Image{Path: file, Name: c.File(), Size: fi.Size()}, nil
}

// verified reports whether file was downloaded and checked against digest.
func verified(file string, digest []byte) bool {
	b, err := os.ReadFile(file + ".verified")
	if err != nil || strings.TrimSpace(string(b)) != hex.EncodeToString(digest) {
		return false
	}
	_, err = os.Stat(file)
	return err == nil
}

// download fetches url to file through a temporary file, checking the digest.
func download(ctx context.Context, url, file, algorithm string, digest []byte) error {
	var h hash.Hash
	switch algorithm {
	case "sha256":
		h = sha256.New()
	case "sha512":
		h = sha512.New()
	default:
		return fmt.Errorf("unknown checksum algorithm %q", algorithm)
	}
	resp, err := get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	tmp, err := os.CreateTemp(filepath.Dir(file), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxImage+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return fmt.Errorf("download %s: %w", url, err)
	case n > maxImage:
		return fmt.Errorf("download %s: larger than %d GiB", url, maxImage>>30)
	case !bytes.Equal(h.Sum(nil), digest):
		return fmt.Errorf("download %s: %s checksum mismatch", url, algorithm)
	}
	if err := os.Rename(tmp.Name(), file); err != nil {
		return err
	}
	return os.WriteFile(file+".verified", []byte(hex.EncodeToString(digest)+"\n"), 0o644)
}

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

// diskFormat returns qcow2 or raw for an image file, by its header.
func diskFormat(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	if bytes.Equal(magic, []byte("QFI\xfb")) {
		return "qcow2", nil
	}
	return "raw", nil
}

// SaveContainer remembers the index entry of a container template, for creates
// without network access.
func SaveContainer(cacheDir string, c Container) error {
	dir := filepath.Join(cacheDir, "containers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, c.Name+".json"), b, 0o644)
}

// LastContainer returns the entry SaveContainer remembered for name.
func LastContainer(cacheDir, name string) (Container, bool) {
	b, err := os.ReadFile(filepath.Join(cacheDir, "containers", name+".json"))
	if err != nil {
		return Container{}, false
	}
	var c Container
	return c, json.Unmarshal(b, &c) == nil && c.Name == name
}
