// Package image downloads, verifies and caches Proxmox VE ISOs and extracts the
// installer kernel, initrd and boot command line from them.
package image

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kdomanski/iso9660"
)

type Image struct {
	Version string // exact release, e.g. 9.2-1
	ISO     string
	Kernel  string
	Initrd  string
	Cmdline string
}

type Logf func(format string, a ...any)

var isoRe = regexp.MustCompile(`^proxmox-ve_([0-9]+\.[0-9]+)-([0-9]+)\.iso$`)

// Ensure makes the ISO for version available in cacheDir and returns the boot files.
func Ensure(ctx context.Context, mirror, version, cacheDir string, logf Logf) (*Image, error) {
	dir := filepath.Join(cacheDir, "iso")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	mirror = strings.TrimRight(mirror, "/")
	sums, err := fetchSums(ctx, mirror, dir)
	if err != nil {
		return nil, err
	}
	file, sum, err := resolve(sums, version)
	if err != nil {
		return nil, err
	}
	iso := filepath.Join(dir, file)
	if err := ensureISO(ctx, mirror+"/"+file, iso, sum, logf); err != nil {
		return nil, err
	}
	img := &Image{Version: strings.TrimSuffix(strings.TrimPrefix(file, "proxmox-ve_"), ".iso"), ISO: iso}
	if err := extract(img, strings.TrimSuffix(iso, ".iso")); err != nil {
		return nil, fmt.Errorf("extract %s: %w", file, err)
	}
	return img, nil
}

func httpGet(ctx context.Context, url string) (*http.Response, error) {
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

// fetchSums downloads SHA256SUMS, falling back to the cached copy when offline.
func fetchSums(ctx context.Context, mirror, dir string) (map[string]string, error) {
	cached := filepath.Join(dir, "SHA256SUMS")
	var data []byte
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := httpGet(tctx, mirror+"/SHA256SUMS")
	if err == nil {
		data, err = io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	if err == nil {
		_ = os.WriteFile(cached, data, 0o644)
	} else if b, cerr := os.ReadFile(cached); cerr == nil {
		data = b
	} else {
		return nil, fmt.Errorf("fetch checksums (set proxmox.mirror if the mirror is unreachable): %w", err)
	}
	return ParseSums(string(data)), nil
}

// ParseSums parses `sha256sum` output into file -> hash.
func ParseSums(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && len(f[0]) == 64 {
			out[strings.TrimPrefix(f[1], "*")] = f[0]
		}
	}
	return out
}

// resolve picks the ISO for "9.2" (newest 9.2-N) or "9.2-1" (exact).
func resolve(sums map[string]string, version string) (file, sum string, err error) {
	best := -1
	for f, s := range sums {
		m := isoRe.FindStringSubmatch(f)
		if m == nil {
			continue
		}
		rel, _ := strconv.Atoi(m[2])
		if (version == m[1]+"-"+m[2] || version == m[1]) && rel > best {
			best, file, sum = rel, f, s
		}
	}
	if file == "" {
		return "", "", fmt.Errorf("no Proxmox VE ISO for version %s in SHA256SUMS", version)
	}
	return file, sum, nil
}

func ensureISO(ctx context.Context, url, path, sum string, logf Logf) error {
	stamp := path + ".sha256"
	if fi, err := os.Stat(path); err == nil {
		if b, _ := os.ReadFile(stamp); strings.TrimSpace(string(b)) == fmt.Sprintf("%s %d", sum, fi.Size()) {
			return nil
		}
		logf("verifying cached %s", filepath.Base(path))
		if got, err := hashFile(path); err == nil && got == sum {
			return os.WriteFile(stamp, []byte(fmt.Sprintf("%s %d\n", sum, fi.Size())), 0o644)
		}
	}
	logf("downloading %s", url)
	resp, err := httpGet(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	part := path + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	defer os.Remove(part)
	h := sha256.New()
	pr := &progress{r: resp.Body, total: resp.ContentLength, logf: logf, last: time.Now()}
	n, err := io.Copy(io.MultiWriter(f, h), pr)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", filepath.Base(path), got, sum)
	}
	if err := os.Rename(part, path); err != nil {
		return err
	}
	logf("downloaded and verified %s (%d MiB)", filepath.Base(path), n>>20)
	return os.WriteFile(stamp, []byte(fmt.Sprintf("%s %d\n", sum, n)), 0o644)
}

type progress struct {
	r     io.Reader
	n     int64
	total int64
	logf  Logf
	last  time.Time
}

func (p *progress) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if time.Since(p.last) > 5*time.Second && p.total > 0 {
		p.last = time.Now()
		p.logf("  %d%% (%d/%d MiB)", p.n*100/p.total, p.n>>20, p.total>>20)
	}
	return n, err
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, bufio.NewReaderSize(f, 1<<20)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var bootFiles = []string{"boot/linux26", "boot/initrd.img", "boot/grub/grub.cfg"}

func extract(img *Image, dir string) error {
	img.Kernel = filepath.Join(dir, "linux26")
	img.Initrd = filepath.Join(dir, "initrd.img")
	grub := filepath.Join(dir, "grub.cfg")
	if !exists(img.Kernel) || !exists(img.Initrd) || !exists(grub) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		f, err := os.Open(img.ISO)
		if err != nil {
			return err
		}
		defer f.Close()
		iso, err := iso9660.OpenImage(f)
		if err != nil {
			return err
		}
		root, err := iso.RootDir()
		if err != nil {
			return err
		}
		for _, p := range bootFiles {
			src, err := lookup(root, p)
			if err != nil {
				return err
			}
			dst := filepath.Join(dir, filepath.Base(p))
			if err := copyOut(src, dst); err != nil {
				return err
			}
		}
	}
	b, err := os.ReadFile(grub)
	if err != nil {
		return err
	}
	img.Cmdline, err = Cmdline(string(b))
	return err
}

// lookup finds a path in the ISO, accepting Rock Ridge and plain ISO9660 names.
func lookup(dir *iso9660.File, path string) (*iso9660.File, error) {
	cur := dir
	for _, part := range strings.Split(path, "/") {
		children, err := cur.GetChildren()
		if err != nil {
			return nil, err
		}
		var next *iso9660.File
		for _, c := range children {
			name := strings.TrimSuffix(strings.Split(c.Name(), ";")[0], ".")
			if strings.EqualFold(name, part) || strings.EqualFold(name, strings.ReplaceAll(part, "-", "_")) {
				next = c
				break
			}
		}
		if next == nil {
			return nil, fmt.Errorf("%s not found in ISO", path)
		}
		cur = next
	}
	return cur, nil
}

func copyOut(src *iso9660.File, dst string) error {
	f, err := os.Create(dst + ".tmp")
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, src.Reader()); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(dst+".tmp", dst)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Cmdline returns the kernel arguments of the automated installer entry in grub.cfg,
// without splash and with the serial console enabled.
func Cmdline(grub string) (string, error) {
	for _, line := range strings.Split(grub, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "linux" || !strings.Contains(line, "proxmox-start-auto-installer") {
			continue
		}
		var args []string
		for _, a := range f[2:] {
			if a != "splash" && !strings.HasPrefix(a, "splash=") && !strings.HasPrefix(a, "console=") {
				args = append(args, a)
			}
		}
		return strings.Join(append(args, "console=ttyS0,115200"), " "), nil
	}
	return "", fmt.Errorf("grub.cfg has no automated installer entry (proxmox-start-auto-installer); ISO too old? Auto-install needs PVE 8.2+")
}
