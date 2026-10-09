package job

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/virtbase/proxbase/internal/progress"
)

// The test binary is the job: it logs a step, a non-JSON line and success.
func TestMain(m *testing.M) {
	if os.Getenv("PROXBASE_FAKE_JOB") == "1" {
		fmt.Fprintln(os.Stderr, `{"type":"step","message":"working","elapsed":1}`)
		fmt.Fprint(os.Stderr, "not json\n")
		time.Sleep(200 * time.Millisecond)
		fmt.Fprintln(os.Stderr, `{"type":"done"}`)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestJob(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PROXBASE_FAKE_JOB", "1")
	if _, err := New("../lab"); err == nil {
		t.Error("New accepted a path as cluster name")
	}
	j, err := New("lab")
	if err != nil {
		t.Fatal(err)
	}
	exited, err := j.Start("create")
	if err != nil {
		t.Fatal(err)
	}
	if j.PID() == 0 {
		t.Error("PID: job not running")
	}
	if _, err := j.Start("create"); err == nil {
		t.Error("second job for the same cluster started")
	}
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("job did not exit")
	}
	if pid := j.PID(); pid != 0 {
		t.Errorf("PID %d after exit", pid)
	}
	events, off, err := j.Events(0)
	if err != nil || len(events) != 3 {
		t.Fatalf("events %v, %v", events, err)
	}
	if events[0].Message != "working" || events[1].Type != progress.TypeLog || events[1].Message != "not json" || events[2].Type != progress.TypeDone {
		t.Errorf("events %+v", events)
	}
	if more, _, _ := j.Events(off); len(more) != 0 {
		t.Errorf("events after the end offset: %v", more)
	}
	j.Remove()
	if _, err := os.Stat(j.Log()); !os.IsNotExist(err) {
		t.Errorf("log not removed: %v", err)
	}
}
