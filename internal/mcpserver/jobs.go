package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/job"
	"github.com/virtbase/proxbase/internal/progress"
)

type waitIn struct {
	Cluster        string `json:"cluster" jsonschema:"cluster name"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"return after this many seconds if the job is still running (default 600, max 3600; at most 50 for clients that cancel tool calls after a minute)"`
}

// jobOut is the state of a job in the background: a create, node add or node remove.
type jobOut struct {
	Cluster string          `json:"cluster"`
	Command string          `json:"command" jsonschema:"the proxbase command the job runs, e.g. create or node add"`
	Running bool            `json:"running" jsonschema:"the job is still running; call cluster_wait"`
	Error   string          `json:"error,omitempty"`
	Hint    string          `json:"hint,omitempty"`
	Log     string          `json:"log" jsonschema:"progress log (JSON lines); node install logs are in the cluster's logs directory"`
	Steps   []string        `json:"steps" jsonschema:"latest progress messages"`
	Status  *cluster.Status `json:"status,omitempty"`
}

// stepsShown is how many progress messages results carry.
const stepsShown = 10

// runJob starts `proxbase args...` as the cluster's background job and follows
// it for timeoutSeconds within the call limit. Cancelling the call stops the
// job (SIGTERM); it saves its state, and running it again resumes.
func (h *handlers) runJob(ctx context.Context, req *mcp.CallToolRequest, j job.Job, args []string, timeoutSeconds int) (jobOut, error) {
	exited, err := j.Start(args...)
	if err != nil {
		return jobOut{}, err
	}
	out, err := h.follow(ctx, req, j, exited, within(h.limit(req), seconds(timeoutSeconds, 600, 3600)))
	if ctx.Err() != nil {
		if serr := j.Signal(syscall.SIGTERM); serr != nil {
			h.log.Logf("cancel %s: %v", j.Command(), serr)
		}
	}
	return out, err
}

func (h *handlers) wait(ctx context.Context, req *mcp.CallToolRequest, in waitIn) (*mcp.CallToolResult, jobOut, error) {
	j, err := job.New(in.Cluster)
	if err != nil {
		return nil, jobOut{}, err
	}
	if _, err := os.Stat(j.Log()); err != nil {
		return nil, jobOut{}, fmt.Errorf("no job was started for cluster %q from MCP; see cluster_status", in.Cluster)
	}
	out, err := h.follow(ctx, req, j, nil, within(h.limit(req), seconds(in.TimeoutSeconds, 600, 3600)))
	return nil, out, err
}

// follow forwards the job's progress until it ends or timeout passes. exited,
// if not nil, is closed when the job (a child of this process) ends.
func (h *handlers) follow(ctx context.Context, req *mcp.CallToolRequest, j job.Job, exited <-chan struct{}, timeout time.Duration) (jobOut, error) {
	p := h.sink(ctx, req)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var offset int64
	var last progress.Event
	forward := func() error {
		events, next, err := j.Events(offset)
		if err != nil {
			return fmt.Errorf("read progress of %s: %w", j.Cluster(), err)
		}
		offset = next
		for _, e := range events {
			p(e)
			last = e
		}
		return nil
	}
	for exited != nil || j.PID() != 0 {
		if err := forward(); err != nil {
			return jobOut{}, err
		}
		select {
		case <-exited:
			exited = nil
		case <-tick.C:
		case <-ctx.Done():
			return jobOut{}, ctx.Err()
		case <-deadline.C:
			out, err := summarize(j)
			out.Running = true
			return out, err
		}
	}
	if err := forward(); err != nil {
		return jobOut{}, err
	}
	out, err := summarize(j)
	if err != nil {
		return out, err
	}
	switch last.Type {
	case progress.TypeError:
		out.Error, out.Hint = last.Error, last.Hint
	case progress.TypeDone:
	default:
		out.Error = "the job ended without a result; see the log"
	}
	if c, err := cluster.Open(j.Cluster(), nil); err == nil {
		out.Status = c.Status(ctx)
	} else if out.Error == "" {
		return out, err
	}
	if out.Error != "" {
		out.Error = toolWords.Replace(out.Error)
		return out, errors.New(out.Error + toolHint(out.Hint))
	}
	return out, nil
}

// toolWords rewrites CLI commands in messages in terms of the tools.
var toolWords = strings.NewReplacer("`proxbase create ", "`cluster_create name=", "`proxbase destroy ", "`cluster_destroy cluster=")

// toolHint rewrites a CLI hint in terms of the tools.
func toolHint(hint string) string {
	if hint == "" {
		return ""
	}
	return "\n" + toolWords.Replace(hint)
}

// summarize returns the latest progress messages of a job.
func summarize(j job.Job) (jobOut, error) {
	out := jobOut{Cluster: j.Cluster(), Command: j.Command(), Log: j.Log(), Running: j.PID() != 0, Steps: []string{}}
	events, _, err := j.Events(0)
	if err != nil {
		return out, fmt.Errorf("read progress of %s: %w", j.Cluster(), err)
	}
	for _, e := range events {
		switch {
		case e.Type == progress.TypeStep:
			out.Steps = append(out.Steps, fmt.Sprintf("[%4.0fs] %s", e.Elapsed, e.Message))
		case e.Type == progress.TypeLog && e.Message != "":
			out.Steps = append(out.Steps, e.Message)
		}
	}
	out.Steps = out.Steps[max(0, len(out.Steps)-stepsShown):]
	return out, nil
}

// stopJob ends a running job: SIGTERM, then SIGKILL after 30 seconds.
func stopJob(ctx context.Context, j job.Job) error {
	if j.PID() == 0 {
		return nil
	}
	if err := j.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop %s: %w", j.Command(), err)
	}
	deadline := time.Now().Add(30 * time.Second)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	killed := false
	for j.PID() != 0 {
		if !killed && time.Now().After(deadline) {
			if err := j.Signal(syscall.SIGKILL); err != nil {
				return fmt.Errorf("kill %s: %w", j.Command(), err)
			}
			killed = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	return nil
}
