package cluster

import (
	"fmt"
	"time"

	"github.com/virtbase/proxbase/internal/job"
	"github.com/virtbase/proxbase/internal/progress"
	"github.com/virtbase/proxbase/internal/state"
)

// SetExpiry makes the cluster expire ttl from now, or never with ttl 0.
func (c *Cluster) SetExpiry(ttl time.Duration) error {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	c.St.ExpiresAt = nil
	Lease{TTL: ttl}.apply(c.St, time.Now())
	return c.save()
}

// GCResult lists what GC deleted and left.
type GCResult struct {
	Deleted []string `json:"deleted" jsonschema:"expired clusters deleted (with dryRun: to be deleted)"`
	Skipped []string `json:"skipped,omitempty" jsonschema:"expired clusters left, with the reason"`
}

// GC deletes the clusters that expired before now, with their job files. It
// leaves clusters whose background job still runs. With dryRun it only reports.
func GC(now time.Time, dryRun bool, p progress.Sink) (GCResult, error) {
	res := GCResult{Deleted: []string{}}
	names, err := state.List()
	if err != nil {
		return res, err
	}
	for _, name := range names {
		st, err := state.ForCluster(name).Load()
		if err != nil || !st.Expired(now) {
			continue
		}
		j, err := job.New(name)
		if err != nil {
			return res, err
		}
		if j.PID() != 0 {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s: %q is running", name, j.Command()))
			continue
		}
		if !dryRun {
			if err := Destroy(name, p); err != nil {
				res.Skipped = append(res.Skipped, fmt.Sprintf("%s: %v", name, err))
				continue
			}
			j.Remove()
		}
		res.Deleted = append(res.Deleted, name)
	}
	return res, nil
}
