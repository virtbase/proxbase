package main

import (
	"compress/gzip"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const template = "../../packaging/" + appID + ".metainfo.xml"

func generate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := run(dir, template, "1.2.3", "2026-10-22T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func manPage(t *testing.T, file string) string {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRun(t *testing.T) {
	dir := generate(t)

	t.Run("man pages", func(t *testing.T) {
		root := manPage(t, filepath.Join(dir, "man", "proxbase.1.gz"))
		for _, want := range []string{`.TH "PROXBASE" "1" "Oct 2026" "proxbase 1.2.3" "Proxbase Manual"`, "\\fBproxbase-create(1)\\fP"} {
			if !strings.Contains(root, want) {
				t.Errorf("proxbase.1 lacks %q:\n%s", want, root)
			}
		}
		if p := manPage(t, filepath.Join(dir, "man", "proxbase-node-add.1.gz")); !strings.Contains(p, "\\fBproxbase node add [cluster] [flags]\\fP") {
			t.Errorf("proxbase-node-add.1 lacks its synopsis:\n%s", p)
		}
		pages, _ := filepath.Glob(filepath.Join(dir, "man", "*"))
		for _, p := range pages {
			if !strings.HasSuffix(p, ".1.gz") {
				t.Errorf("%s is not a gzipped section 1 page", p)
			}
			if strings.Contains(p, "switch") {
				t.Errorf("%s documents a hidden command", p)
			}
		}
	})

	t.Run("completions", func(t *testing.T) {
		for name, want := range map[string]string{
			"proxbase.bash": "__start_proxbase",
			"_proxbase":     "#compdef proxbase",
			"proxbase.fish": "complete -c proxbase",
		} {
			b, err := os.ReadFile(filepath.Join(dir, "completions", name))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
	})

	t.Run("metainfo", func(t *testing.T) {
		b, err := os.ReadFile(filepath.Join(dir, "metainfo", appID+".metainfo.xml"))
		if err != nil {
			t.Fatal(err)
		}
		var c struct {
			Type     string `xml:"type,attr"`
			ID       string `xml:"id"`
			Binary   string `xml:"provides>binary"`
			Icon     string `xml:"icon"`
			Releases []struct {
				Version string `xml:"version,attr"`
				Date    string `xml:"date,attr"`
				URL     string `xml:"url"`
			} `xml:"releases>release"`
		}
		if err := xml.Unmarshal(b, &c); err != nil {
			t.Fatal(err)
		}
		if c.Type != "console-application" || c.ID != appID || c.Binary != "proxbase" || c.Icon != appID {
			t.Errorf("component %+v", c)
		}
		if len(c.Releases) != 1 || c.Releases[0].Version != "1.2.3" || c.Releases[0].Date != "2026-10-22" ||
			c.Releases[0].URL != "https://github.com/virtbase/proxbase/releases/tag/v1.2.3" {
			t.Errorf("releases %+v", c.Releases)
		}
	})

	t.Run("reproducible", func(t *testing.T) {
		again := generate(t)
		files, _ := filepath.Glob(filepath.Join(dir, "*", "*"))
		for _, f := range files {
			rel, _ := filepath.Rel(dir, f)
			a, _ := os.ReadFile(f)
			b, err := os.ReadFile(filepath.Join(again, rel))
			if err != nil || string(a) != string(b) {
				t.Errorf("%s differs between runs", rel)
			}
		}
	})
}

func TestDebianChangelog(t *testing.T) {
	dir := t.TempDir()
	if err := debianChangelog(dir, "0.4.0-rc.1", time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	want := `proxbase (0.4.0~rc.1) unstable; urgency=medium

  * Release 0.4.0-rc.1, see the release notes:
    https://github.com/virtbase/proxbase/releases/tag/v0.4.0-rc.1

 -- Janic Bellmann <contact@janic.dev>  Thu, 22 Oct 2026 12:00:00 +0000
`
	if got := manPage(t, filepath.Join(dir, "changelog.Debian.gz")); got != want {
		t.Errorf("changelog.Debian.gz:\n%s\nwant:\n%s", got, want)
	}
}

func TestReleaseDate(t *testing.T) {
	tests := []struct {
		name, date, epoch string
		want              time.Time
		wantErr           bool
	}{
		{name: "flag", date: "2026-10-22T12:00:00Z", epoch: "1", want: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)},
		{name: "SOURCE_DATE_EPOCH", epoch: "1792670400", want: time.Unix(1792670400, 0)},
		{name: "invalid flag", date: "2026-10-22", wantErr: true},
		{name: "invalid SOURCE_DATE_EPOCH", epoch: "soon", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SOURCE_DATE_EPOCH", tt.epoch)
			got, err := releaseDate(tt.date)
			if (err != nil) != tt.wantErr || !tt.wantErr && !got.Equal(tt.want) {
				t.Errorf("releaseDate(%q) = %v, %v", tt.date, got, err)
			}
		})
	}
}

func TestMetainfoFileRejectsReleases(t *testing.T) {
	file := filepath.Join(t.TempDir(), "in.xml")
	if err := os.WriteFile(file, []byte(`<component><releases/></component>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := metainfoFile(file, t.TempDir(), "1.0.0", time.Now()); err == nil {
		t.Error("a template with releases was accepted")
	}
}

// goreleaserConfig holds the parts of .goreleaser.yaml that gendist must agree with.
type goreleaserConfig struct {
	Casks []struct {
		Manpages []string `yaml:"manpages"`
	} `yaml:"homebrew_casks"`
	NFPMs []struct {
		Maintainer string `yaml:"maintainer"`
	} `yaml:"nfpms"`
}

func readGoreleaser(t *testing.T) goreleaserConfig {
	t.Helper()
	b, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cfg goreleaserConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Casks) != 1 || len(cfg.NFPMs) != 1 {
		t.Fatalf("%d casks and %d nfpms, want one each", len(cfg.Casks), len(cfg.NFPMs))
	}
	return cfg
}

func TestMaintainer(t *testing.T) {
	if got := readGoreleaser(t).NFPMs[0].Maintainer; got != maintainer {
		t.Errorf(".goreleaser.yaml nfpms maintainer = %q, gendist writes %q", got, maintainer)
	}
}

// TestCaskManpages checks that the Homebrew cask lists every generated man page:
// casks take paths, not globs.
func TestCaskManpages(t *testing.T) {
	cfg := readGoreleaser(t)
	pages, _ := filepath.Glob(filepath.Join(generate(t), "man", "*.1.gz"))
	var want []string
	for _, p := range pages {
		want = append(want, "man/"+filepath.Base(p))
	}
	got := slices.Sorted(slices.Values(cfg.Casks[0].Manpages))
	if !slices.Equal(got, want) {
		t.Errorf(".goreleaser.yaml homebrew_casks manpages = %v,\nwant %v", got, want)
	}
}
