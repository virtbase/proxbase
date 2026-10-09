// Package mcpserver exposes proxbase to AI agents as a Model Context Protocol
// server. Clients start it as `proxbase mcp` and talk to it over stdio.
package mcpserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/progress"
	"github.com/virtbase/proxbase/internal/state"
)

// SchemaURI is the resource with the JSON schema of cluster files.
const SchemaURI = "https://proxbase.virtbase.com/schema/v1alpha1/cluster.json"

const instructions = `proxbase creates Proxmox VE clusters out of QEMU/KVM VMs on this host, for tests and labs.
Every node is reachable on 127.0.0.1 through forwarded ports (web UI/API and SSH).

Typical flow:
1. cluster_create: blocks and reports progress. The first create downloads the ISO and builds a
   base image (10-20 minutes); later creates clone it (a few minutes). If it returns with
   running=true, call cluster_wait until running=false.
2. cluster_status: healthy=true means ready, quorate, all nodes online, Ceph HEALTH_OK.
3. node_exec runs shell commands as root on a node (e.g. pvesh, qm, pct, ceph).
   cluster_env returns the API endpoint, token and CA certificate for API clients.
4. To reset quickly between tests: cluster_stop, snapshot_save once, then snapshot_restore + cluster_start.
5. fault_apply/fault_clear inject failures (power loss, hangs, cable pulls, partitions, latency).
6. cluster_destroy deletes everything; it needs confirm set to the cluster name.

Clusters persist across sessions; call cluster_list first to reuse one.
The cluster file format is the resource ` + SchemaURI + `.`

// New returns the server. log receives the progress of all operations as well
// (e.g. for the client's server log on stderr).
func New(version string, log progress.Sink) *mcp.Server {
	if log == nil {
		log = progress.Discard
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "proxbase", Title: "Proxbase", Version: version},
		&mcp.ServerOptions{Instructions: instructions})
	h := &handlers{log: log}
	h.register(s)
	s.AddResource(&mcp.Resource{
		URI:         SchemaURI,
		Name:        "cluster-schema",
		Title:       "Cluster file JSON schema",
		Description: "JSON schema of proxbase cluster files (apiVersion proxbase.virtbase.com/v1alpha1, kind Cluster)",
		MIMEType:    "application/schema+json",
	}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		b, err := config.Schema()
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: SchemaURI, MIMEType: "application/schema+json", Text: string(b)}}}, nil
	})
	return s
}

// Run serves one client on stdin/stdout until it disconnects.
func Run(ctx context.Context, version string, log progress.Sink) error {
	return New(version, log).Run(ctx, &mcp.StdioTransport{})
}

type handlers struct{ log progress.Sink }

// sink forwards progress to the client (if it asked for it) and the server log.
func (h *handlers) sink(ctx context.Context, req *mcp.CallToolRequest) progress.Sink {
	token := req.Params.GetProgressToken()
	if token == nil || req.Session == nil {
		return h.log
	}
	var mu sync.Mutex
	n := 0
	return progress.Tee(h.log, func(e progress.Event) {
		msg := e.Message
		switch {
		case e.Type == progress.TypeStep:
			msg = fmt.Sprintf("[%4.0fs] %s", e.Elapsed, e.Message)
		case e.Type == progress.TypeError:
			msg = "error: " + e.Error
		case msg == "":
			return
		}
		// Held while sending: progress values must arrive in increasing order.
		mu.Lock()
		defer mu.Unlock()
		n++
		if err := req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: token, Progress: float64(n), Message: msg}); err != nil && ctx.Err() == nil {
			h.log.Logf("progress notification: %v", err)
		}
	})
}

// clusterName picks the given name, else "default", else the only cluster.
func clusterName(name string) string {
	if name != "" {
		return name
	}
	if names, _ := state.List(); len(names) == 1 && !state.ForCluster("default").Exists() {
		return names[0]
	}
	return "default"
}

func (h *handlers) open(name string, p progress.Sink) (*cluster.Cluster, error) {
	return cluster.Open(clusterName(name), p)
}

func seconds(v, def, max int) time.Duration {
	if v <= 0 {
		v = def
	}
	return time.Duration(min(v, max)) * time.Second
}

func ptr[T any](v T) *T { return &v }
