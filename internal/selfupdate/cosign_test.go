package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCosign writes a cosign stand-in that saves its arguments and the files it
// was given, and exits with code.
func fakeCosign(t *testing.T, code string) (bin, record string) {
	t.Helper()
	dir := t.TempDir()
	bin, record = filepath.Join(dir, "cosign"), filepath.Join(dir, "record")
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + record + `"
for a in "$@"; do [ -f "$a" ] && cat "$a" >> "` + record + `"; done
echo "error: none of the expected identities matched" >&2
exit ` + code + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, record
}

func TestCosign(t *testing.T) {
	t.Parallel()
	t.Run("valid signature", func(t *testing.T) {
		t.Parallel()
		bin, record := fakeCosign(t, "0")
		if err := Cosign(bin)(t.Context(), []byte("SUMS"), []byte("BUNDLE")); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(record)
		if err != nil {
			t.Fatal(err)
		}
		got := string(b)
		for _, want := range []string{
			"verify-blob\n--bundle\n",
			"--certificate-identity-regexp\n" + certificateIdentity + "\n",
			"--certificate-oidc-issuer\nhttps://token.actions.githubusercontent.com\n",
			"BUNDLE", "SUMS",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("cosign got %q, want it to contain %q", got, want)
			}
		}
	})
	t.Run("invalid signature", func(t *testing.T) {
		t.Parallel()
		bin, _ := fakeCosign(t, "1")
		err := Cosign(bin)(t.Context(), []byte("SUMS"), []byte("BUNDLE"))
		if err == nil || !strings.Contains(err.Error(), "none of the expected identities matched") {
			t.Errorf("Cosign error = %v, want cosign's message", err)
		}
	})
}
