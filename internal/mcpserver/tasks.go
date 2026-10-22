package mcpserver

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/pve"
)

const (
	taskListDescription = `List the Proxmox VE tasks of a cluster, newest first: VM and container operations, migrations,
backups, storage and Ceph jobs. Reads every node, or one. Filter by errorsOnly, running, type (e.g. qmstart,
qmigrate, vzdump), vmid and sinceMinutes. task_log shows a task's log, task_wait waits for one to end.`
	taskLogDescription  = `Return the status and the end of the log of a Proxmox VE task, by its UPID (from task_list, pve_call or an error).`
	taskWaitDescription = `Wait for a Proxmox VE task (UPID) to end and return its exit status and the end of its log; a failed
task is an error. Use it for tasks that pve_call or task_list reported as running.`
)

type taskListIn struct {
	Cluster      string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Node         string `json:"node,omitempty" jsonschema:"only tasks of this node (default: every node)"`
	ErrorsOnly   bool   `json:"errorsOnly,omitempty" jsonschema:"only tasks that failed"`
	Running      bool   `json:"running,omitempty" jsonschema:"only tasks that are still running"`
	Type         string `json:"type,omitempty" jsonschema:"only tasks of this type, e.g. qmstart, qmigrate, vzdump"`
	VMID         int    `json:"vmid,omitempty" jsonschema:"only tasks of this guest"`
	SinceMinutes int    `json:"sinceMinutes,omitempty" jsonschema:"only tasks started in the last this many minutes"`
	Limit        int    `json:"limit,omitempty" jsonschema:"most tasks to return (default 20, max 200)"`
}

type taskListOut struct {
	Tasks       []taskEntry `json:"tasks"`
	Unavailable []string    `json:"unavailable,omitempty" jsonschema:"nodes whose tasks could not be read, with the reason"`
}

type taskEntry struct {
	UPID    string `json:"upid"`
	Node    string `json:"node"`
	Type    string `json:"type"`
	ID      string `json:"id,omitempty" jsonschema:"guest or object the task works on"`
	User    string `json:"user"`
	Status  string `json:"status" jsonschema:"running, OK, WARNINGS: n, or the error"`
	Started string `json:"started" jsonschema:"RFC 3339"`
	Ended   string `json:"ended,omitempty" jsonschema:"RFC 3339"`
	Seconds int64  `json:"seconds" jsonschema:"how long it ran, or has been running"`
}

type taskLogIn struct {
	Cluster string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	UPID    string `json:"upid" jsonschema:"task ID, e.g. UPID:pve1:000A12B3:0012C4D5:65000000:qmstart:100:root@pam:"`
	Lines   int    `json:"lines,omitempty" jsonschema:"last lines to return (default 50, max 2000)"`
}

type taskLogOut struct {
	UPID       string   `json:"upid"`
	Node       string   `json:"node"`
	Status     string   `json:"status" jsonschema:"running or stopped"`
	ExitStatus string   `json:"exitStatus,omitempty" jsonschema:"OK, WARNINGS: n, or the error"`
	Log        []string `json:"log"`
	TotalLines int      `json:"totalLines" jsonschema:"lines the log has; log holds the last of them"`
}

type taskWaitIn struct {
	Cluster        string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	UPID           string `json:"upid" jsonschema:"task ID"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"wait at most this long (default 120, max 3600; at most 50 for clients that cancel tool calls after a minute)"`
}

const (
	// defaultTasks and maxTasks bound task_list.
	defaultTasks = 20
	maxTasks     = 200
	// defaultLogLines and maxLogLines bound task_log.
	defaultLogLines = 50
	maxLogLines     = 2000
	// taskListTimeout bounds reading the task lists of the nodes.
	taskListTimeout = 15 * time.Second
)

func (h *handlers) taskList(ctx context.Context, req *mcp.CallToolRequest, in taskListIn) (*mcp.CallToolResult, taskListOut, error) {
	c, err := h.open(in.Cluster, nil)
	if err != nil {
		return nil, taskListOut{}, err
	}
	nodes := []string{in.Node}
	if in.Node == "" {
		nodes = nodes[:0]
		for _, n := range c.St.Nodes {
			nodes = append(nodes, n.Name)
		}
	} else if c.St.Node(in.Node) == nil {
		return nil, taskListOut{}, fmt.Errorf("cluster %s has no node %q", c.Cfg.Name, in.Node)
	}
	api, err := c.TokenAPI("")
	if err != nil {
		return nil, taskListOut{}, err
	}
	f := pve.TaskFilter{
		Limit: min(cmp.Or(max(in.Limit, 0), defaultTasks), maxTasks), Errors: in.ErrorsOnly, Running: in.Running,
		Type: in.Type, VMID: in.VMID,
	}
	if in.SinceMinutes > 0 {
		f.Since = time.Now().Add(-time.Duration(in.SinceMinutes) * time.Minute)
	}
	// A node that is cut off makes the API wait 30 seconds for it.
	ctx, cancel := context.WithTimeout(ctx, within(h.limit(req), taskListTimeout))
	defer cancel()

	results := make([][]pve.Task, len(nodes))
	errs := make([]error, len(nodes))
	var wg sync.WaitGroup
	for i, node := range nodes {
		if !c.NodeRunning(node) {
			errs[i] = errors.New("not running")
			continue
		}
		wg.Go(func() { results[i], errs[i] = api.Tasks(ctx, node, f) })
	}
	wg.Wait()

	out := taskListOut{Tasks: []taskEntry{}}
	var tasks []pve.Task
	for i, node := range nodes {
		if errs[i] != nil {
			out.Unavailable = append(out.Unavailable, fmt.Sprintf("%s: %v", node, errs[i]))
			continue
		}
		tasks = append(tasks, results[i]...)
	}
	slices.SortStableFunc(tasks, func(a, b pve.Task) int { return cmp.Compare(b.StartTime, a.StartTime) })
	now := time.Now()
	for _, t := range tasks[:min(len(tasks), f.Limit)] {
		out.Tasks = append(out.Tasks, newTaskEntry(t, now))
	}
	return nil, out, nil
}

// newTaskEntry describes a task as of now.
func newTaskEntry(t pve.Task, now time.Time) taskEntry {
	start := time.Unix(t.StartTime, 0).UTC()
	e := taskEntry{UPID: t.UPID, Node: t.Node, Type: t.Type, ID: t.ID, User: t.User, Status: t.Status, Started: start.Format(time.RFC3339)}
	if t.Running() {
		e.Status = "running"
		e.Seconds = int64(now.Sub(start).Seconds())
		return e
	}
	end := time.Unix(t.EndTime, 0).UTC()
	e.Ended = end.Format(time.RFC3339)
	e.Seconds = int64(end.Sub(start).Seconds())
	return e
}

func (h *handlers) taskLog(ctx context.Context, req *mcp.CallToolRequest, in taskLogIn) (*mcp.CallToolResult, taskLogOut, error) {
	api, node, err := h.taskAPI(in.Cluster, in.UPID)
	if err != nil {
		return nil, taskLogOut{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, within(h.limit(req), apiRequestTimeout))
	defer cancel()
	st, err := api.TaskStatus(ctx, node, in.UPID)
	if err != nil {
		return nil, taskLogOut{}, fmt.Errorf("task %s: %w", in.UPID, err)
	}
	lines := min(cmp.Or(max(in.Lines, 0), defaultLogLines), maxLogLines)
	log, total, err := api.TaskLogTail(ctx, node, in.UPID, lines)
	if err != nil {
		return nil, taskLogOut{}, fmt.Errorf("log of task %s: %w", in.UPID, err)
	}
	return nil, taskLogOut{UPID: in.UPID, Node: node, Status: st.Status, ExitStatus: st.ExitStatus, Log: log, TotalLines: total}, nil
}

func (h *handlers) taskWait(ctx context.Context, req *mcp.CallToolRequest, in taskWaitIn) (*mcp.CallToolResult, taskOut, error) {
	api, node, err := h.taskAPI(in.Cluster, in.UPID)
	if err != nil {
		return nil, taskOut{}, err
	}
	deadline := time.Now().Add(within(h.limit(req), seconds(in.TimeoutSeconds, 120, 3600)))
	task, err := waitTask(ctx, api, node, in.UPID, deadline.Add(-taskPollTimeout))
	if err != nil {
		return nil, taskOut{}, err
	}
	return nil, *task, nil
}

// taskAPI returns an API client of the cluster and the node that runs upid.
func (h *handlers) taskAPI(clusterName, upid string) (*pve.Client, string, error) {
	node, ok := pve.TaskNode(upid)
	if !ok {
		return nil, "", fmt.Errorf("%q is not a task ID (UPID:node:...)", upid)
	}
	c, err := h.open(clusterName, nil)
	if err != nil {
		return nil, "", err
	}
	api, err := c.TokenAPI("")
	return api, node, err
}
