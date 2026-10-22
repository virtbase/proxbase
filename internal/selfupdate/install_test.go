package selfupdate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetector_detect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		path    string
		env     map[string]string
		owner   Method // what the package manager query reports
		want    Method
		wantErr bool
	}{
		{name: "release archive", path: "/usr/local/bin/proxbase", want: Binary},
		{name: "home directory", path: "/home/me/.local/bin/proxbase", env: map[string]string{"HOME": "/home/me"}, want: Binary},
		{name: "deb package", path: "/usr/bin/proxbase", owner: Deb, want: Deb},
		{name: "rpm package", path: "/usr/bin/proxbase", owner: RPM, want: RPM},
		{name: "apk package", path: "/usr/bin/proxbase", owner: APK, want: APK},
		{name: "homebrew cask on macos", path: "/opt/homebrew/Caskroom/proxbase/0.2.2/proxbase", want: Homebrew},
		{name: "homebrew cask on linux", path: "/home/linuxbrew/.linuxbrew/Caskroom/proxbase/0.2.2/proxbase", want: Homebrew},
		{name: "homebrew formula", path: "/usr/local/Cellar/proxbase/0.2.2/bin/proxbase", want: Homebrew},
		{name: "go install default gopath", path: "/home/me/go/bin/proxbase", env: map[string]string{"HOME": "/home/me"}, want: GoInstall},
		{name: "go install gopath list", path: "/src/b/bin/proxbase", env: map[string]string{"GOPATH": "/src/a:/src/b"}, want: GoInstall},
		{name: "go install gobin", path: "/opt/gobin/proxbase", env: map[string]string{"GOBIN": "/opt/gobin/"}, want: GoInstall},
		{
			name: "github action",
			path: "/home/runner/work/_temp/proxbase-bin/proxbase",
			env:  map[string]string{"GITHUB_ACTIONS": "true", "RUNNER_TEMP": "/home/runner/work/_temp"},
			want: Action,
		},
		{
			name: "own install on a runner",
			path: "/usr/local/bin/proxbase",
			env:  map[string]string{"GITHUB_ACTIONS": "true", "RUNNER_TEMP": "/home/runner/work/_temp"},
			want: Binary,
		},
		{name: "container image", path: "/usr/local/bin/proxbase", env: map[string]string{MethodEnv: "container"}, want: Container},
		{name: "override a package", path: "/usr/bin/proxbase", owner: Deb, env: map[string]string{MethodEnv: "binary"}, want: Binary},
		{name: "invalid override", path: "/usr/bin/proxbase", env: map[string]string{MethodEnv: "snap"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := detector{
				getenv: func(k string) string { return tt.env[k] },
				owner: func(_ context.Context, path string) Method {
					if path != tt.path {
						t.Errorf("owner(%q), want %q", path, tt.path)
					}
					return tt.owner
				},
			}
			got, err := d.detect(t.Context(), tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("detect(%q) error = %v, want error %v", tt.path, err, tt.wantErr)
			}
			if err == nil && (got.Method != tt.want || got.Path != tt.path) {
				t.Errorf("detect(%q) = %+v, want method %s", tt.path, got, tt.want)
			}
		})
	}
}

func TestPackageOwner(t *testing.T) {
	t.Parallel()
	if got := packageOwner(t.Context(), filepath.Join(t.TempDir(), "proxbase")); got != "" {
		t.Errorf("packageOwner(new file) = %q, want none", got)
	}
	if _, err := exec.LookPath("dpkg-query"); err != nil {
		t.Skip("no dpkg on this host")
	}
	if _, err := os.Stat("/usr/bin/dpkg"); err != nil {
		t.Skip("no /usr/bin/dpkg")
	}
	if got := packageOwner(t.Context(), "/usr/bin/dpkg"); got != Deb {
		t.Errorf("packageOwner(/usr/bin/dpkg) = %q, want %q", got, Deb)
	}
}

func TestInstall_Instructions(t *testing.T) {
	t.Parallel()
	deb := assetName("v0.2.2", "deb")
	rel := &Release{Tag: "v0.2.2", Assets: []Asset{{Name: deb, URL: "https://example.com/" + deb}}}
	sum := strings.Repeat("a", 64)
	tests := []struct {
		name   string
		method Method
		sums   map[string]string
		want   []string // lines
	}{
		{name: "binary", method: Binary, want: []string{""}},
		{name: "homebrew", method: Homebrew, want: []string{"brew upgrade --cask virtbase/tap/proxbase"}},
		{name: "go install", method: GoInstall, want: []string{"go install github.com/virtbase/proxbase/cmd/proxbase@v0.2.2"}},
		{name: "container", method: Container, want: []string{"docker pull ghcr.io/virtbase/proxbase:0.2.2, then recreate the container from it"}},
		{
			name:   "deb with checksum",
			method: Deb,
			sums:   map[string]string{deb: sum},
			want: []string{
				"curl -fsSLO https://example.com/" + deb,
				"echo '" + sum + "  " + deb + "' | sha256sum -c",
				"sudo apt install ./" + deb,
			},
		},
		{
			name:   "rpm not in the release",
			method: RPM,
			want: []string{
				"curl -fsSLO https://github.com/virtbase/proxbase/releases/download/v0.2.2/" + assetName("v0.2.2", "rpm"),
				"sudo rpm --upgrade ./" + assetName("v0.2.2", "rpm"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Install{Method: tt.method}.Instructions(rel, tt.sums)
			if want := strings.Join(tt.want, "\n"); got != want {
				t.Errorf("Instructions() =\n%s\nwant\n%s", got, want)
			}
		})
	}
}
