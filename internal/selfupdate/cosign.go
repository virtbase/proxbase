package selfupdate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The release workflow signs checksums.txt keyless with its GitHub OIDC identity;
// the GitHub Action and docs/install.md check the same identity.
const (
	certificateIdentity = `^https://github\.com/virtbase/proxbase/\.github/workflows/release\.yml@`
	certificateIssuer   = "https://token.actions.githubusercontent.com"
	cosignTimeout       = 2 * time.Minute
)

// Cosign returns a Verifier that runs `cosign verify-blob` (the binary at path) and
// accepts only signatures made by the release workflow of virtbase/proxbase.
func Cosign(path string) Verifier {
	return func(ctx context.Context, checksums, bundle []byte) error {
		dir, err := os.MkdirTemp("", "proxbase-upgrade-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		sums, b := filepath.Join(dir, ChecksumsFile), filepath.Join(dir, BundleFile)
		if err := os.WriteFile(sums, checksums, 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(b, bundle, 0o600); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, cosignTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, path, "verify-blob",
			"--bundle", b,
			"--certificate-identity-regexp", certificateIdentity,
			"--certificate-oidc-issuer", certificateIssuer,
			sums,
		).CombinedOutput()
		if err != nil {
			return fmt.Errorf("cosign verify-blob: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
}
