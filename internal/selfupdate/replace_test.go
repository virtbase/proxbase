package selfupdate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const oldBinary = "#!/bin/sh\necho proxbase 0.2.0\n"

// fakeBinary is a script that reports version like proxbase version does.
func fakeBinary(version string) string { return "#!/bin/sh\necho proxbase " + version + "\n" }

// install writes the old binary into a new directory and returns its path.
func install(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proxbase")
	if err := os.WriteFile(path, []byte(oldBinary), 0o750); err != nil {
		t.Fatal(err)
	}
	return path
}

// assertOnly checks that dir holds just the files names, so no temporary file is left.
func assertOnly(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != strings.Join(names, ",") {
		t.Errorf("%s has %v, want %v", dir, got, names)
	}
}

func TestReplace(t *testing.T) {
	t.Parallel()
	path := install(t)
	archive := tarGz(t, map[string]string{"LICENSE": "Apache", "README.md": "#", "proxbase": fakeBinary("0.2.2")})
	if err := Replace(t.Context(), path, archive, "v0.2.2"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != fakeBinary("0.2.2") {
		t.Errorf("binary = %q, %v, want the new one", b, err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o750 {
		t.Errorf("mode = %v, %v, want 0750 of the old binary", fi.Mode(), err)
	}
	assertOnly(t, filepath.Dir(path), "proxbase")
}

func TestReplace_ResolvedSymlink(t *testing.T) {
	t.Parallel()
	target := install(t)
	link := filepath.Join(t.TempDir(), "proxbase")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	archive := tarGz(t, map[string]string{"proxbase": fakeBinary("0.2.2")})
	if err := Replace(t.Context(), link, archive, "v0.2.2"); err == nil {
		t.Fatal("Replace of a symlink succeeded, want an error (it would replace the link)")
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := Replace(t.Context(), resolved, archive, "v0.2.2"); err != nil {
		t.Fatal(err)
	}
	if dest, err := os.Readlink(link); err != nil || dest != target {
		t.Errorf("link points to %q, %v, want %q", dest, err, target)
	}
	if b, _ := os.ReadFile(link); string(b) != fakeBinary("0.2.2") {
		t.Errorf("binary behind the link = %q, want the new one", b)
	}
}

func TestReplace_Failures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		archive func(t *testing.T) []byte
		want    string
	}{
		{name: "not gzip", archive: func(*testing.T) []byte { return []byte("not an archive") }, want: "read archive"},
		{
			name:    "no binary",
			archive: func(t *testing.T) []byte { return tarGz(t, map[string]string{"README.md": "#"}) },
			want:    "archive has no proxbase binary",
		},
		{
			name:    "binary in a subdirectory",
			archive: func(t *testing.T) []byte { return tarGz(t, map[string]string{"bin/proxbase": fakeBinary("0.2.2")}) },
			want:    "archive has no proxbase binary",
		},
		{
			name:    "wrong version",
			archive: func(t *testing.T) []byte { return tarGz(t, map[string]string{"proxbase": fakeBinary("0.1.0")}) },
			want:    "want version v0.2.2",
		},
		{
			name:    "does not run",
			archive: func(t *testing.T) []byte { return tarGz(t, map[string]string{"proxbase": "#!/bin/sh\nexit 3\n"}) },
			want:    "does not run",
		},
		{
			name: "truncated",
			archive: func(t *testing.T) []byte {
				b := tarGz(t, map[string]string{"proxbase": strings.Repeat("x", 1<<16)})
				return b[:len(b)/2]
			},
			want: "read archive",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := install(t)
			err := Replace(t.Context(), path, tt.archive(t), "v0.2.2")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Replace error = %v, want %q", err, tt.want)
			}
			if b, _ := os.ReadFile(path); string(b) != oldBinary {
				t.Errorf("binary = %q after a failed Replace, want the old one", b)
			}
			assertOnly(t, filepath.Dir(path), "proxbase")
		})
	}
}

func TestCheckWritable(t *testing.T) {
	t.Parallel()
	path := install(t)
	if err := CheckWritable(path); err != nil {
		t.Errorf("CheckWritable(%s) = %v", path, err)
	}
	assertOnly(t, filepath.Dir(path), "proxbase")
	if os.Geteuid() == 0 {
		t.Skip("root can write read-only directories")
	}
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := CheckWritable(path); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("CheckWritable in a read-only directory = %v, want fs.ErrPermission", err)
	}
	err := Replace(t.Context(), path, tarGz(t, map[string]string{"proxbase": fakeBinary("0.2.2")}), "v0.2.2")
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Replace in a read-only directory = %v, want fs.ErrPermission", err)
	}
}
