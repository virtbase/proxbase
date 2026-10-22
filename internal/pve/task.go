package pve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TaskStatus is the state of a task.
type TaskStatus struct {
	Status     string `json:"status"` // "running" or "stopped"
	ExitStatus string `json:"exitstatus,omitempty"`
}

// Stopped reports whether the task has ended.
func (s TaskStatus) Stopped() bool { return s.Status == "stopped" }

// OK reports whether a stopped task succeeded, possibly with warnings.
func (s TaskStatus) OK() bool {
	return s.ExitStatus == "OK" || strings.HasPrefix(s.ExitStatus, "WARNINGS")
}

// TaskNode returns the node that runs the task upid ("UPID:node:pid:...").
func TaskNode(upid string) (string, bool) {
	f := strings.SplitN(upid, ":", 3)
	if len(f) < 3 || f[0] != "UPID" || f[1] == "" {
		return "", false
	}
	return f[1], true
}

func taskPath(node, upid string) string {
	return "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid)
}

// TaskStatus returns the state of a task.
func (c *Client) TaskStatus(ctx context.Context, node, upid string) (TaskStatus, error) {
	var st TaskStatus
	err := c.Get(ctx, taskPath(node, upid)+"/status", &st)
	return st, err
}

// TaskLogTail returns the last n lines of a task's log and how many it has.
func (c *Client) TaskLogTail(ctx context.Context, node, upid string, n int) ([]string, int, error) {
	var probe []json.RawMessage
	total, err := c.GetPage(ctx, taskPath(node, upid)+"/log?limit=1", &probe)
	if err != nil {
		return nil, 0, err
	}
	var lines []struct {
		T string `json:"t"`
	}
	q := url.Values{"start": {strconv.Itoa(max(0, total-n))}, "limit": {strconv.Itoa(n)}}
	if _, err := c.GetPage(ctx, taskPath(node, upid)+"/log?"+q.Encode(), &lines); err != nil {
		return nil, 0, err
	}
	log := make([]string, len(lines))
	for i, l := range lines {
		log[i] = l.T
	}
	return log, total, nil
}

// Task is an entry of a node's task list.
type Task struct {
	UPID      string `json:"upid"`
	Node      string `json:"node"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	User      string `json:"user"`
	Status    string `json:"status"` // exit status once ended; "RUNNING" or empty before
	StartTime int64  `json:"starttime"`
	EndTime   int64  `json:"endtime"`
}

// Running reports whether the task has not ended yet.
func (t Task) Running() bool { return t.EndTime == 0 }

// Failed reports whether the task ended without success.
func (t Task) Failed() bool {
	return !t.Running() && !TaskStatus{Status: "stopped", ExitStatus: t.Status}.OK()
}

// TaskFilter selects tasks of a node.
type TaskFilter struct {
	Limit   int       // most tasks to return
	Errors  bool      // only tasks that failed
	Running bool      // only tasks that are still running
	Type    string    // only this type, e.g. qmstart
	VMID    int       // only tasks of this guest
	Since   time.Time // only tasks started since then
}

// Tasks returns the tasks of a node, newest first, from its archive and the
// running ones.
func (c *Client) Tasks(ctx context.Context, node string, f TaskFilter) ([]Task, error) {
	q := url.Values{"source": {"all"}}
	if f.Running {
		q.Set("source", "active")
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	if f.Errors {
		q.Set("errors", "1")
	}
	if f.Type != "" {
		q.Set("typefilter", f.Type)
	}
	if f.VMID > 0 {
		q.Set("vmid", strconv.Itoa(f.VMID))
	}
	if !f.Since.IsZero() {
		q.Set("since", strconv.FormatInt(f.Since.Unix(), 10))
	}
	var tasks []Task
	err := c.Get(ctx, "/nodes/"+url.PathEscape(node)+"/tasks?"+q.Encode(), &tasks)
	return tasks, err
}

// PostTask POSTs and waits for the task if the call started one.
func (c *Client) PostTask(ctx context.Context, node, path string, form url.Values, timeout time.Duration) error {
	return c.doTask(ctx, http.MethodPost, node, path, form, timeout)
}

// DeleteTask DELETEs and waits for the task if the call started one.
func (c *Client) DeleteTask(ctx context.Context, node, path string, timeout time.Duration) error {
	return c.doTask(ctx, http.MethodDelete, node, path, nil, timeout)
}

func (c *Client) doTask(ctx context.Context, method, node, path string, form url.Values, timeout time.Duration) error {
	var upid string
	if err := c.Do(ctx, method, path, form, &upid); err != nil {
		return err
	}
	if upid == "" {
		return nil
	}
	return c.WaitTask(ctx, node, upid, timeout)
}

// Member is an entry of /cluster/status.
type Member struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Online  int    `json:"online"`
	Quorate int    `json:"quorate"`
	Nodes   int    `json:"nodes"`
	IP      string `json:"ip"`
}

// ClusterStatus returns the cluster entry (nil before a cluster exists) and the nodes by name.
func (c *Client) ClusterStatus(ctx context.Context) (*Member, map[string]Member, error) {
	var entries []Member
	if err := c.Get(ctx, "/cluster/status", &entries); err != nil {
		return nil, nil, err
	}
	var cluster *Member
	nodes := map[string]Member{}
	for i, e := range entries {
		switch e.Type {
		case "cluster":
			cluster = &entries[i]
		case "node":
			nodes[e.Name] = e
		}
	}
	return cluster, nodes, nil
}
