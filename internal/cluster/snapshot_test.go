package cluster

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/state"
	"github.com/virtbase/proxbase/internal/vm"
)

// fakeRuntime records live snapshot calls; every node runs.
type fakeRuntime struct {
	mu      sync.Mutex
	calls   []string // "pause pve1", "save pve2", ...
	failOn  string   // node whose SaveLive fails
	deleted []string
}

var (
	_ vm.Runtime         = (*fakeRuntime)(nil)
	_ vm.LiveSnapshotter = (*fakeRuntime)(nil)
)

func (f *fakeRuntime) record(op string, s vm.Spec) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, op+" "+s.Name)
}

func (f *fakeRuntime) Install(context.Context, vm.Spec, vm.Media) error                 { return nil }
func (f *fakeRuntime) Start(context.Context, vm.Spec) error                             { return nil }
func (f *fakeRuntime) Stop(context.Context, vm.Spec, time.Duration) error               { return nil }
func (f *fakeRuntime) Running(vm.Spec) bool                                             { return true }
func (f *fakeRuntime) Snapshot(context.Context, vm.SnapshotOp, string, []vm.Disk) error { return nil }

func (f *fakeRuntime) Pause(_ context.Context, s vm.Spec) error  { f.record("pause", s); return nil }
func (f *fakeRuntime) Resume(_ context.Context, s vm.Spec) error { f.record("resume", s); return nil }

func (f *fakeRuntime) SaveLive(_ context.Context, s vm.Spec, _ string) error {
	f.record("save", s)
	if s.Name == f.failOn {
		return errors.New("disk full")
	}
	return nil
}

func (f *fakeRuntime) DeleteLive(_ context.Context, s vm.Spec, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, s.Name)
	return nil
}

// newTestCluster returns a ready 3-node cluster in a temporary data directory.
func newTestCluster(t *testing.T, rt vm.Runtime) *Cluster {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg, err := config.Decode(strings.NewReader("name: snap\nnodes: {count: 3}\n"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetDefaults()
	c, err := initialize(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.runtime = rt
	c.St.Phase = state.PhaseReady
	return c
}

func (f *fakeRuntime) ops(op string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if name, ok := strings.CutPrefix(c, op+" "); ok {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func TestCluster_SnapshotSaveLive(t *testing.T) {
	rt := &fakeRuntime{}
	c := newTestCluster(t, rt)
	if err := c.SnapshotSaveLive(t.Context(), "ready"); err != nil {
		t.Fatal(err)
	}
	all := []string{"pve1", "pve2", "pve3"}
	for _, op := range []string{"pause", "save", "resume"} {
		if got := rt.ops(op); !slices.Equal(got, all) {
			t.Errorf("%s: %v, want %v", op, got, all)
		}
	}
	// Every node is paused before the first snapshot is written.
	firstSave := slices.IndexFunc(rt.calls, func(s string) bool { return strings.HasPrefix(s, "save ") })
	if paused := len(rt.ops("pause")); firstSave < paused {
		t.Errorf("a snapshot was saved before all nodes were paused: %v", rt.calls)
	}
	snap := c.St.Snapshot("ready")
	if snap == nil || !snap.Live {
		t.Fatalf("snapshot not recorded as live: %+v", c.St.Snapshots)
	}
}

func TestCluster_SnapshotSaveLiveRollsBack(t *testing.T) {
	rt := &fakeRuntime{failOn: "pve2"}
	c := newTestCluster(t, rt)
	if err := c.SnapshotSaveLive(t.Context(), "ready"); err == nil {
		t.Fatal("expected the failed save to fail the snapshot")
	}
	if got := rt.ops("resume"); !slices.Equal(got, []string{"pve1", "pve2", "pve3"}) {
		t.Errorf("not every node was resumed: %v", got)
	}
	rt.mu.Lock()
	deleted := slices.Sorted(slices.Values(rt.deleted))
	rt.mu.Unlock()
	saved := slices.DeleteFunc(rt.ops("save"), func(n string) bool { return n == "pve2" })
	if !slices.Equal(deleted, saved) {
		t.Errorf("deleted %v, want the snapshots that were saved: %v", deleted, saved)
	}
	if c.St.Snapshot("ready") != nil {
		t.Error("a failed snapshot must not be recorded")
	}
}

func TestCluster_SnapshotSaveLiveRefusesFaults(t *testing.T) {
	rt := &fakeRuntime{}
	c := newTestCluster(t, rt)
	c.St.Faults = []state.Fault{{Kind: "freeze", Node: "pve1"}}
	if err := c.SnapshotSaveLive(t.Context(), "ready"); err == nil || !strings.Contains(err.Error(), "faults") {
		t.Fatalf("got %v, want an error about active faults", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("nodes were touched: %v", rt.calls)
	}
}
