package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

const (
	binaryName = "proxbase"
	maxBinary  = 512 << 20 // uncompressed; the binary is about 15 MiB
)

// CheckWritable reports whether a file can be created next to path, so that a
// missing permission fails before the download.
func CheckWritable(path string) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".check-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

// Replace installs the proxbase binary from archive, a release .tar.gz, at path
// (a regular file, symlinks resolved). It writes the binary to a temporary file in
// the same directory with the mode and owner of path, syncs it, checks that
// `<binary> version` reports version, and renames it over path, which is atomic:
// path is the old or the new binary at any time, and a failure leaves it alone.
// Running processes keep executing the old file.
func Replace(ctx context.Context, path string, archive []byte, version string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() { // a symlink would be replaced by a file
		return fmt.Errorf("%s is not a regular file", path)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".new-*")
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err := extract(archive, tmp); err != nil {
		return err
	}
	mode := fi.Mode().Perm()
	if mode&0o100 == 0 {
		mode = 0o755
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	// As root (sudo), keep the owner of the file being replaced.
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && os.Geteuid() == 0 {
		if err := tmp.Chown(int(st.Uid), int(st.Gid)); err != nil {
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := checkBinary(ctx, tmp.Name(), version); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	renamed = true
	syncDir(dir)
	return nil
}

// extract copies the proxbase binary out of a .tar.gz archive to w.
func extract(archive []byte, w io.Writer) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("archive has no %s binary", binaryName)
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if path.Clean(h.Name) != binaryName || h.Typeflag != tar.TypeReg {
			continue
		}
		if h.Size > maxBinary {
			return fmt.Errorf("%s in the archive is %d bytes, more than the limit of %d", binaryName, h.Size, maxBinary)
		}
		if _, err := io.CopyN(w, tr, h.Size); err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		return nil
	}
}

// checkBinary runs `<bin> version` and expects it to print version.
func checkBinary(ctx context.Context, bin, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	for i := 0; errors.Is(err, syscall.ETXTBSY) && i < 5; i++ {
		// A process forked meanwhile may still hold the file open for
		// writing until it execs (golang/go#22315).
		time.Sleep(50 * time.Millisecond)
		out, err = exec.CommandContext(ctx, bin, "version").Output()
	}
	if err != nil {
		return fmt.Errorf("the new binary does not run (%s version): %w", bin, err)
	}
	want := strings.TrimPrefix(version, "v")
	if !slices.ContainsFunc(strings.Fields(string(out)), func(f string) bool { return strings.TrimPrefix(f, "v") == want }) {
		return fmt.Errorf("the new binary reports %q, want version %s", strings.TrimSpace(string(out)), version)
	}
	return nil
}

// syncDir makes a rename in dir durable where the file system supports syncing
// a directory. The rename is done either way, so errors do not matter.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}
