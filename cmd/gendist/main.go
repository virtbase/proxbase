// Command gendist writes the files that release archives and packages ship next to
// the binary: a man page per command, shell completions and the AppStream metainfo.
// GoReleaser runs it before the builds:
//
//	go run ./cmd/gendist -version 0.3.0 -date 2026-10-22T12:00:00Z build
//
// It writes dir/man/*.1.gz, dir/completions/{proxbase.bash,_proxbase,proxbase.fish},
// dir/metainfo/<id>.metainfo.xml and dir/deb/changelog.Debian.gz. It is not part of proxbase itself, so that the
// released binary does not link the man page renderer.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/virtbase/proxbase/internal/cli"
)

const (
	// appID is the AppStream component ID, also the icon name.
	appID = "io.github.virtbase.proxbase"
	// maintainer is the nfpm maintainer in .goreleaser.yaml.
	maintainer = "Janic Bellmann <contact@janic.dev>"
)

func main() {
	version := flag.String("version", "dev", "version for the man pages and the metainfo release")
	date := flag.String("date", "", "release date in RFC 3339 (default: now, or SOURCE_DATE_EPOCH)")
	metainfo := flag.String("metainfo", filepath.Join("packaging", appID+".metainfo.xml"), "metainfo template")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: gendist [flags] <dir>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(flag.Arg(0), *metainfo, *version, *date); err != nil {
		fmt.Fprintln(os.Stderr, "gendist:", err)
		os.Exit(1)
	}
}

func run(dir, metainfo, version, date string) error {
	t, err := releaseDate(date)
	if err != nil {
		return err
	}
	root := cli.Command()
	root.InitDefaultCompletionCmd()
	if err := manPages(root, filepath.Join(dir, "man"), version, t); err != nil {
		return fmt.Errorf("man pages: %w", err)
	}
	if err := completions(root, filepath.Join(dir, "completions")); err != nil {
		return fmt.Errorf("completions: %w", err)
	}
	if err := metainfoFile(metainfo, filepath.Join(dir, "metainfo"), version, t); err != nil {
		return fmt.Errorf("metainfo: %w", err)
	}
	if err := debianChangelog(filepath.Join(dir, "deb"), version, t); err != nil {
		return fmt.Errorf("deb changelog: %w", err)
	}
	return nil
}

// releaseDate parses date, defaulting to SOURCE_DATE_EPOCH and then the current time.
func releaseDate(date string) (time.Time, error) {
	if date != "" {
		return time.Parse(time.RFC3339, date)
	}
	if epoch := os.Getenv("SOURCE_DATE_EPOCH"); epoch != "" {
		var sec int64
		if _, err := fmt.Sscan(epoch, &sec); err != nil {
			return time.Time{}, fmt.Errorf("invalid SOURCE_DATE_EPOCH %q", epoch)
		}
		return time.Unix(sec, 0), nil
	}
	return time.Now(), nil
}

// emptyDir creates dir, removing files a previous run left in it.
func emptyDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

// manPages writes a gzipped page per visible command, e.g. proxbase-node-add.1.gz.
func manPages(root *cobra.Command, dir, version string, date time.Time) error {
	if err := emptyDir(dir); err != nil {
		return err
	}
	root.DisableAutoGenTag = true
	header := &doc.GenManHeader{Section: "1", Source: "proxbase " + version, Manual: "Proxbase Manual", Date: &date}
	if err := doc.GenManTree(root, header, dir); err != nil {
		return err
	}
	pages, err := filepath.Glob(filepath.Join(dir, "*.1"))
	if err != nil {
		return err
	}
	for _, p := range pages {
		if err := gzipFile(p); err != nil {
			return err
		}
	}
	return nil
}

// gzipFile replaces file with file.gz, without a name or time in the header.
func gzipFile(file string) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return err
	}
	if _, err := zw.Write(b); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(file+".gz", buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Remove(file)
}

func completions(root *cobra.Command, dir string) error {
	if err := emptyDir(dir); err != nil {
		return err
	}
	for name, gen := range map[string]func(*bytes.Buffer) error{
		"proxbase.bash": func(b *bytes.Buffer) error { return root.GenBashCompletionV2(b, true) },
		"_proxbase":     func(b *bytes.Buffer) error { return root.GenZshCompletion(b) },
		"proxbase.fish": func(b *bytes.Buffer) error { return root.GenFishCompletion(b, true) },
	} {
		var b bytes.Buffer
		if err := gen(&b); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// metainfoFile copies the template with a <releases> entry for version added.
func metainfoFile(template, dir, version string, date time.Time) error {
	b, err := os.ReadFile(template)
	if err != nil {
		return err
	}
	s := string(b)
	if strings.Contains(s, "<releases") {
		return errors.New(template + " must not list releases; the release is added for the version built")
	}
	end := strings.LastIndex(s, "</component>")
	if end < 0 {
		return errors.New(template + " has no </component>")
	}
	var esc bytes.Buffer
	if err := xml.EscapeText(&esc, []byte(version)); err != nil {
		return err
	}
	v := esc.String()
	releases := fmt.Sprintf(`  <releases>
    <release version="%s" date="%s">
      <url type="details">https://github.com/virtbase/proxbase/releases/tag/v%s</url>
    </release>
  </releases>
`, v, date.UTC().Format(time.DateOnly), v)
	if err := emptyDir(dir); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, appID+".metainfo.xml"), []byte(s[:end]+releases+s[end:]), 0o644) //nolint:gosec // build tool: dir is its argument
}

// debianChangelog writes a changelog.Debian.gz with one entry that links the
// release notes, as Debian policy asks of every package.
func debianChangelog(dir, version string, date time.Time) error {
	if err := emptyDir(dir); err != nil {
		return err
	}
	file := filepath.Join(dir, "changelog.Debian")
	entry := fmt.Sprintf(`proxbase (%s) unstable; urgency=medium

  * Release %s, see the release notes:
    https://github.com/virtbase/proxbase/releases/tag/v%s

 -- %s  %s
`, debVersion(version), version, version, maintainer, date.UTC().Format(time.RFC1123Z))
	if err := os.WriteFile(file, []byte(entry), 0o644); err != nil {
		return err
	}
	return gzipFile(file)
}

// debVersion is version as nfpm writes it into a .deb: a pre-release sorts before
// the release, so 0.4.0-rc.1 becomes 0.4.0~rc.1.
func debVersion(version string) string { return strings.Replace(version, "-", "~", 1) }
