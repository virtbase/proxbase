package pve

import (
	"context"
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

// TaskLog returns the first limit lines of a task's log.
func (c *Client) TaskLog(ctx context.Context, node, upid string, limit int) ([]string, error) {
	var lines []struct {
		T string `json:"t"`
	}
	if err := c.Get(ctx, taskPath(node, upid)+"/log?limit="+strconv.Itoa(limit), &lines); err != nil {
		return nil, err
	}
	log := make([]string, len(lines))
	for i, l := range lines {
		log[i] = l.T
	}
	return log, nil
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
