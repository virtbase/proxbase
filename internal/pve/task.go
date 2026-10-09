package pve

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

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
