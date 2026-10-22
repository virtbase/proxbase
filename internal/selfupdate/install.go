package selfupdate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Method is how the running proxbase was installed.
type Method string

const (
	Binary    Method = "binary"    // a release archive or a local build: upgraded in place
	Homebrew  Method = "homebrew"  // brew install --cask virtbase/tap/proxbase
	Deb       Method = "deb"       // the release's .deb package
	RPM       Method = "rpm"       // the release's .rpm package
	APK       Method = "apk"       // the release's .apk package
	GoInstall Method = "go"        // go install into $GOBIN or $GOPATH/bin
	Container Method = "container" // the ghcr.io/virtbase/proxbase image
	Action    Method = "action"    // installed by the virtbase/proxbase GitHub Action
)

var methods = []Method{Binary, Homebrew, Deb, RPM, APK, GoInstall, Container, Action}

// MethodEnv overrides the detected Method. The container image sets it to
// container; set it to binary to upgrade an install detected wrongly in place.
const MethodEnv = "PROXBASE_INSTALL_METHOD"

// Label names the method in a sentence: "installed with <label>".
func (m Method) Label() string {
	switch m {
	case Homebrew:
		return "Homebrew"
	case Deb:
		return "a .deb package"
	case RPM:
		return "an .rpm package"
	case APK:
		return "an .apk package"
	case GoInstall:
		return "go install"
	case Container:
		return "the container image"
	case Action:
		return "the virtbase/proxbase GitHub Action"
	}
	return "a release archive"
}

// Install is the running proxbase binary.
type Install struct {
	Path   string // the executable, symlinks resolved
	Method Method
}

// Detect finds the running executable and how it was installed.
func Detect(ctx context.Context) (Install, error) {
	exe, err := os.Executable()
	if err != nil {
		return Install{}, err
	}
	path, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return Install{}, err
	}
	return detector{getenv: os.Getenv, owner: packageOwner}.detect(ctx, path)
}

type detector struct {
	getenv func(string) string
	owner  func(ctx context.Context, path string) Method // package manager owning path, or ""
}

func (d detector) detect(ctx context.Context, path string) (Install, error) {
	in := Install{Path: path, Method: Binary}
	if m := Method(d.getenv(MethodEnv)); m != "" {
		if !slices.Contains(methods, m) {
			return Install{}, fmt.Errorf("%s=%s: want one of %v", MethodEnv, m, methods)
		}
		in.Method = m
		return in, nil
	}
	dir := filepath.Dir(path)
	switch {
	case d.getenv("GITHUB_ACTIONS") == "true" && d.getenv("RUNNER_TEMP") != "" &&
		dir == filepath.Join(d.getenv("RUNNER_TEMP"), "proxbase-bin"):
		in.Method = Action
	case strings.Contains(path, "/Caskroom/") || strings.Contains(path, "/Cellar/"):
		in.Method = Homebrew
	case slices.Contains(d.goBinDirs(), dir):
		in.Method = GoInstall
	default:
		if m := d.owner(ctx, path); m != "" {
			in.Method = m
		}
	}
	return in, nil
}

// goBinDirs are the directories go install writes to.
func (d detector) goBinDirs() []string {
	var dirs []string
	if gobin := d.getenv("GOBIN"); gobin != "" {
		dirs = append(dirs, filepath.Clean(gobin))
	}
	gopath := d.getenv("GOPATH")
	if gopath == "" && d.getenv("HOME") != "" {
		gopath = filepath.Join(d.getenv("HOME"), "go")
	}
	for p := range strings.SplitSeq(gopath, string(filepath.ListSeparator)) {
		if p != "" {
			dirs = append(dirs, filepath.Join(p, "bin"))
		}
	}
	return dirs
}

// packageOwner asks dpkg, rpm and apk whether one of their packages owns path.
func packageOwner(ctx context.Context, path string) Method {
	queries := []struct {
		method Method
		cmd    string
		args   []string
	}{
		{Deb, "dpkg-query", []string{"--search", path}},
		{RPM, "rpm", []string{"--query", "--file", path}},
		{APK, "apk", []string{"info", "--who-owns", path}},
	}
	for _, q := range queries {
		bin, err := exec.LookPath(q.cmd)
		if err != nil {
			continue
		}
		qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = exec.CommandContext(qctx, bin, q.args...).Run()
		cancel()
		if err == nil {
			return q.method
		}
	}
	return ""
}

// Instructions tells how to upgrade an install that proxbase must not replace
// itself to rel; sums (may be nil) adds a checksum check for packages. It
// returns "" for Binary.
func (in Install) Instructions(rel *Release, sums map[string]string) string {
	switch in.Method {
	case Homebrew:
		return "brew upgrade --cask virtbase/tap/proxbase"
	case GoInstall:
		return "go install github.com/virtbase/proxbase/cmd/proxbase@" + rel.Tag
	case Container:
		return fmt.Sprintf("docker pull ghcr.io/virtbase/proxbase:%s, then recreate the container from it", strings.TrimPrefix(rel.Tag, "v"))
	case Action:
		return fmt.Sprintf("set the action's version input to %s (the default, latest, installs the newest release)", rel.Tag)
	case Deb, RPM, APK:
		name := assetName(rel.Tag, string(in.Method))
		u := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", Repo, rel.Tag, name)
		if a, err := rel.Asset(name); err == nil {
			u = a.URL
		}
		lines := []string{"curl -fsSLO " + u}
		if sum, ok := sums[name]; ok {
			lines = append(lines, fmt.Sprintf("echo '%s  %s' | sha256sum -c", sum, name))
		}
		install := map[Method]string{
			Deb: "sudo apt install ./",
			RPM: "sudo rpm --upgrade ./",
			APK: "sudo apk add --allow-untrusted ./",
		}[in.Method]
		return strings.Join(append(lines, install+name), "\n")
	}
	return ""
}
