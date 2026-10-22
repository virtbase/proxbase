package cluster

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/virtbase/proxbase/internal/progress"
	"github.com/virtbase/proxbase/internal/state"
)

func TestLeaseApply(t *testing.T) {
	now := time.Unix(1000, 0)
	st := &state.State{}
	Lease{Owner: "claude-code", TTL: time.Hour}.apply(st, now)
	if st.Owner != "claude-code" || !st.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("first lease: %q %v", st.Owner, st.ExpiresAt)
	}
	Lease{Owner: "alice"}.apply(st, now.Add(time.Minute))
	if st.Owner != "claude-code" || !st.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Errorf("a later lease without TTL changed the owner or expiry: %q %v", st.Owner, st.ExpiresAt)
	}
	Lease{TTL: 2 * time.Hour}.apply(st, now.Add(time.Minute))
	if !st.ExpiresAt.Equal(now.Add(time.Minute + 2*time.Hour)) {
		t.Errorf("a later TTL did not extend the expiry: %v", st.ExpiresAt)
	}
}

func TestGC(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	now := time.Now()
	past, future := now.Add(-time.Minute), now.Add(time.Hour)
	for name, expires := range map[string]*time.Time{"old": &past, "busy": &past, "fresh": &future, "keep": nil} {
		cfg := newConfig(t, name, 1, 18000)
		saveCluster(t, cfg)
		if err := state.ForCluster(name).Save(&state.State{Name: name, Phase: state.PhaseReady, ExpiresAt: expires}); err != nil {
			t.Fatal(err)
		}
	}
	// busy has a running job: a process that looks like one to job.PID.
	jobs := filepath.Join(state.DataHome(), "jobs")
	if err := os.MkdirAll(jobs, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "sleep 30", "--progress", "json")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if err := os.WriteFile(filepath.Join(jobs, "busy.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobs, "old.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := GC(now, true, progress.Discard)
	if err != nil || !slices.Equal(res.Deleted, []string{"old"}) || len(res.Skipped) != 1 || !strings.HasPrefix(res.Skipped[0], "busy:") {
		t.Fatalf("dry run: %+v, %v", res, err)
	}
	if !state.ForCluster("old").Exists() {
		t.Fatal("dry run deleted a cluster")
	}
	if res, err = GC(now, false, progress.Discard); err != nil || !slices.Equal(res.Deleted, []string{"old"}) {
		t.Fatalf("gc: %+v, %v", res, err)
	}
	names, _ := state.List()
	if !slices.Equal(names, []string{"busy", "fresh", "keep"}) {
		t.Errorf("clusters after gc: %v", names)
	}
	if _, err := os.Stat(filepath.Join(jobs, "old.jsonl")); !os.IsNotExist(err) {
		t.Errorf("job log of the deleted cluster kept: %v", err)
	}
}
