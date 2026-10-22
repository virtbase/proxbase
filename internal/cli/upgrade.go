package cli

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/virtbase/proxbase/internal/selfupdate"
)

// exitUpdateAvailable is the exit code of upgrade --check when there is a newer
// release (100, like dnf check-update).
const exitUpdateAvailable = 100

type upgradeOptions struct {
	version        string // release tag; "" for the latest
	signature      string // auto, require or skip
	check          bool
	yes            bool
	allowDowngrade bool
}

func upgradeCmd() *cobra.Command {
	var o upgradeOptions
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade proxbase itself to the latest release",
		Long: `upgrade replaces this proxbase binary with the latest GitHub release (or
--version), after checking the download against the release's checksums.txt and,
if cosign is installed, the signature of checksums.txt.

The new binary is renamed over the old one, so the file is always complete.
Running clusters are not affected: their switch processes and a running
proxbase mcp keep the old version until they restart.

Installs that a package manager owns are not replaced; upgrade prints the
command to run instead: Homebrew, the .deb/.rpm/.apk packages, go install, the
container image and the GitHub Action. Set PROXBASE_INSTALL_METHOD=binary to
upgrade such an install in place anyway.

--check only reports: exit code 0 when up to date, 100 when a newer release
exists, 1 on errors. GITHUB_TOKEN (or GH_TOKEN) raises the GitHub API rate limit.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runUpgrade(cmd, o) },
	}
	cmd.Flags().StringVar(&o.version, "version", "", "release to install, e.g. v0.3.0 (default: the latest)")
	cmd.Flags().BoolVar(&o.check, "check", false, "only report whether a newer release exists (exit code 100 if so)")
	cmd.Flags().BoolVar(&o.yes, "yes", false, "do not ask for confirmation (needed without a terminal)")
	cmd.Flags().BoolVar(&o.allowDowngrade, "allow-downgrade", false, "install --version even if it is older than this binary")
	cmd.Flags().StringVar(&o.signature, "signature", "auto", "verify the signature of checksums.txt with cosign: auto (if installed), require or skip")
	return cmd
}

func runUpgrade(cmd *cobra.Command, o upgradeOptions) error {
	switch o.signature {
	case "auto", "require", "skip":
	default:
		return fmt.Errorf("--signature must be auto, require or skip")
	}
	ctx, out := cmd.Context(), cmd.OutOrStdout()
	in, err := selfupdate.Detect(ctx)
	if err != nil {
		return err
	}
	c := upgradeClient()
	rel, err := c.Release(ctx, o.version)
	if err != nil {
		return err
	}
	current := versionTag(version)
	switch change := selfupdate.Classify(version, rel.Tag); {
	case change == selfupdate.Same:
		fmt.Fprintf(out, "proxbase %s is up to date\n", rel.Tag)
		return nil
	case change == selfupdate.Downgrade && (o.version == "" || o.check):
		fmt.Fprintf(out, "proxbase %s is newer than the release %s\n", current, rel.Tag)
		return nil
	case change == selfupdate.Downgrade && !o.allowDowngrade:
		return &hintError{
			err:  fmt.Errorf("%s is older than this proxbase %s", rel.Tag, current),
			hint: "Pass --allow-downgrade to install it anyway.",
		}
	}
	if o.check {
		fmt.Fprintf(out, "proxbase %s is available (this is %s)\n", rel.Tag, current)
		if how := in.Instructions(rel, nil); how != "" {
			fmt.Fprintf(out, "upgrade with: %s\n", strings.ReplaceAll(how, "\n", " && "))
		}
		return exitCode(exitUpdateAvailable)
	}
	if in.Method != selfupdate.Binary {
		return managedInstall(ctx, c, in, rel, o.signature)
	}
	if err := selfupdate.CheckWritable(in.Path); err != nil {
		return replaceError(in.Path, err)
	}
	if !o.yes {
		if err := confirm(cmd.InOrStdin(), fmt.Sprintf("Replace proxbase %s at %s with %s?", current, in.Path, rel.Tag)); err != nil {
			return err
		}
	}
	return replaceBinary(ctx, c, in.Path, rel, o.signature)
}

// replaceBinary downloads and verifies rel and installs it at path.
func replaceBinary(ctx context.Context, c *selfupdate.Client, path string, rel *selfupdate.Release, signature string) error {
	verify, err := signatureVerifier(signature)
	if err != nil {
		return err
	}
	sums, err := c.Checksums(ctx, rel, verify)
	if err != nil {
		return err
	}
	if verify != nil {
		logf("verified the signature of %s with cosign", selfupdate.ChecksumsFile)
	}
	name := selfupdate.ArchiveName(rel.Tag)
	logf("downloading %s", name)
	archive, err := c.Download(ctx, rel, name, sums)
	if err != nil {
		return err
	}
	logf("verified %s (%.1f MiB) against %s", name, float64(len(archive))/(1<<20), selfupdate.ChecksumsFile)
	if err := selfupdate.Replace(ctx, path, archive, rel.Tag); err != nil {
		return replaceError(path, err)
	}
	logf("installed proxbase %s at %s (was %s)", rel.Tag, path, versionTag(version))
	return nil
}

// versionTag shows a version like a tag, e.g. v0.2.2 for GoReleaser's 0.2.2.
func versionTag(v string) string {
	if _, err := selfupdate.ParseVersion(v); err == nil {
		return "v" + strings.TrimPrefix(v, "v")
	}
	return v
}

// upgradeClient talks to github.com, or to PROXBASE_UPGRADE_URL (a mirror of the
// GitHub API or a test server), which never gets the GitHub token.
func upgradeClient() *selfupdate.Client {
	c := selfupdate.NewClient("proxbase/" + version)
	if u := os.Getenv("PROXBASE_UPGRADE_URL"); u != "" {
		c.API = u
	} else {
		c.Token = cmp.Or(os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN"))
	}
	return c
}

// signatureVerifier returns the cosign verifier for --signature, or nil to check
// the SHA-256 only.
func signatureVerifier(mode string) (selfupdate.Verifier, error) {
	if mode == "skip" {
		return nil, nil
	}
	path, err := exec.LookPath("cosign")
	switch {
	case err == nil:
		return selfupdate.Cosign(path), nil
	case mode == "require":
		return nil, &hintError{
			err:  fmt.Errorf("--signature require: %w", err),
			hint: "Install cosign (https://docs.sigstore.dev/cosign/system_config/installation/), or use --signature auto.",
		}
	}
	logf("cosign not found: checking SHA-256 only (install cosign to also verify the release signature)")
	return nil, nil
}

// managedInstall refuses to replace a binary that a package manager owns and
// tells how to upgrade it instead.
func managedInstall(ctx context.Context, c *selfupdate.Client, in selfupdate.Install, rel *selfupdate.Release, signature string) error {
	var sums map[string]string
	switch in.Method {
	case selfupdate.Deb, selfupdate.RPM, selfupdate.APK:
		verify, err := signatureVerifier(signature)
		if err != nil {
			return err
		}
		if sums, err = c.Checksums(ctx, rel, verify); err != nil {
			return err
		}
	}
	how := in.Instructions(rel, sums)
	return &hintError{
		err: fmt.Errorf("%s was installed with %s; upgrade it the same way", in.Path, in.Method.Label()),
		hint: fmt.Sprintf("To upgrade to %s:\n\n  %s\n\nIf proxbase was not installed this way, upgrade it in place with %s=binary.",
			rel.Tag, strings.ReplaceAll(how, "\n", "\n  "), selfupdate.MethodEnv),
	}
}

// replaceError explains a missing permission to replace path.
func replaceError(path string, err error) error {
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	return &hintError{
		err:  fmt.Errorf("cannot replace %s: %w", path, err),
		hint: fmt.Sprintf("Run the upgrade as the owner of %s, e.g.:\n\n  sudo %s upgrade", filepath.Dir(path), path),
	}
}

// confirm asks a yes/no question on the terminal.
func confirm(in io.Reader, question string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("refusing to replace the binary without --yes (stdin is not a terminal)")
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	line, _ := bufio.NewReader(in).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		return errors.New("aborted")
	}
	return nil
}
