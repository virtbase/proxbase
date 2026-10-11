package qemu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/virtbase/proxbase/internal/vm"
)

var _ vm.LiveSnapshotter = Runtime{}

const liveSnapshotTimeout = 10 * time.Minute

// SaveLive saves an internal snapshot of every disk with the node's memory (in
// the root disk) through a QMP snapshot-save job. QEMU stops the vCPUs while it
// saves and resumes them afterwards, unless the node was paused.
func (Runtime) SaveLive(ctx context.Context, s vm.Spec, name string) error {
	q, err := dialQMP(qmpSocket(s))
	if err != nil {
		return fmt.Errorf("%s: %w", s.Name, err)
	}
	defer q.Close()
	nodes, err := diskNodes(q, s)
	if err != nil {
		return fmt.Errorf("%s: %w", s.Name, err)
	}
	job := "snapshot-save-" + name
	if _, err := q.Execute("snapshot-save", map[string]any{"job-id": job, "tag": name, "vmstate": nodes[0], "devices": nodes}); err != nil {
		return fmt.Errorf("%s: %w", s.Name, err)
	}
	if err := waitJob(ctx, q, job, liveSnapshotTimeout); err != nil {
		return fmt.Errorf("%s: snapshot %s: %w", s.Name, name, err)
	}
	return nil
}

// DeleteLive deletes a snapshot from every disk of a running node through a
// QMP snapshot-delete job. Disks without the snapshot are no error.
func (Runtime) DeleteLive(ctx context.Context, s vm.Spec, name string) error {
	q, err := dialQMP(qmpSocket(s))
	if err != nil {
		return fmt.Errorf("%s: %w", s.Name, err)
	}
	defer q.Close()
	nodes, err := diskNodes(q, s)
	if err != nil {
		return fmt.Errorf("%s: %w", s.Name, err)
	}
	job := "snapshot-delete-" + name
	if _, err := q.Execute("snapshot-delete", map[string]any{"job-id": job, "tag": name, "devices": nodes}); err != nil {
		return fmt.Errorf("%s: %w", s.Name, err)
	}
	if err := waitJob(ctx, q, job, liveSnapshotTimeout); err != nil {
		return fmt.Errorf("%s: delete snapshot %s: %w", s.Name, name, err)
	}
	return nil
}

// Pause stops all vCPUs.
func (Runtime) Pause(_ context.Context, s vm.Spec) error { return qmpExec(qmpSocket(s), "stop", nil) }

// Resume lets the vCPUs run again.
func (Runtime) Resume(_ context.Context, s vm.Spec) error { return qmpExec(qmpSocket(s), "cont", nil) }

// diskNodes returns the block node names of the root disk and the data disks,
// in that order.
func diskNodes(q *qmpConn, s vm.Spec) ([]string, error) {
	raw, err := q.Execute("query-block", nil)
	if err != nil {
		return nil, err
	}
	var blocks []struct {
		Device   string `json:"device"`
		Inserted *struct {
			NodeName string `json:"node-name"`
		} `json:"inserted"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	byDevice := map[string]string{}
	for _, b := range blocks {
		if b.Inserted != nil {
			byDevice[b.Device] = b.Inserted.NodeName
		}
	}
	ids := []string{"root"}
	for i := range s.DataDisks {
		ids = append(ids, fmt.Sprintf("data%d", i))
	}
	nodes := make([]string, 0, len(ids))
	for _, id := range ids {
		n, ok := byDevice[id]
		if !ok {
			return nil, fmt.Errorf("disk %s not found", id)
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// waitJob waits until a QMP job has concluded, returns its error and dismisses it.
func waitJob(ctx context.Context, q *qmpConn, id string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		raw, err := q.Execute("query-jobs", nil)
		if err != nil {
			return err
		}
		var jobs []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(raw, &jobs); err != nil {
			return err
		}
		for _, j := range jobs {
			if j.ID != id || j.Status != "concluded" {
				continue
			}
			_, derr := q.Execute("job-dismiss", map[string]any{"id": id})
			if j.Error != "" {
				return errors.New(j.Error)
			}
			return derr
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("job %s: %w", id, ctx.Err())
		case <-tick.C:
		}
	}
}
