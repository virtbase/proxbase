package ceph

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The script sets each memory target once and keeps values the user set.
func TestMemoryTargetScript(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		dump string
		want string
	}{
		{
			name: "fresh cluster",
			dump: `[]`,
			want: "config set osd osd_memory_target 2147483648\nconfig set mds mds_cache_memory_limit 1073741824\n",
		},
		{
			name: "osd target set by the user",
			dump: `[{"section": "osd", "name": "osd_memory_target", "value": "8589934592"}]`,
			want: "config set mds mds_cache_memory_limit 1073741824\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			log := filepath.Join(dir, "calls")
			fake := "#!/bin/sh\n" +
				"if [ \"$1 $2\" = 'config dump' ]; then echo '" + tt.dump + "'; exit 0; fi\n" +
				"echo \"$*\" >> " + log + "\n"
			if err := os.WriteFile(filepath.Join(dir, "ceph"), []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "bash", "-euo", "pipefail", "-s")
			cmd.Stdin = strings.NewReader(memoryTargetScript())
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("script failed: %v\n%s", err, out)
			}
			got, err := os.ReadFile(log)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("ceph calls:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}
