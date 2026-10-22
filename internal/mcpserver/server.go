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
   When something fails, cluster_diagnose collects quorum, failed units and tasks, service
   errors, clock offsets and Ceph health of every node in one call.
3. pve_get and pve_call use the Proxmox VE API with the cluster's token (no shell quoting);
   pve_call waits for the task it starts. node_exec runs shell commands as root on a node
   (e.g. qm, pct, ceph, journalctl). cluster_env returns the API endpoint, token and CA
   certificate for your own API clients.
4. To reset quickly between tests: cluster_stop, snapshot_save once, then snapshot_restore + cluster_start.
5. fault_apply/fault_clear inject failures (power loss, hangs, cable pulls, partitions, latency).
6. cluster_destroy deletes everything; it needs confirm set to the cluster name.

Clusters persist across sessions; call cluster_list first to reuse one.
The cluster file format is the resource ` + SchemaURI + `.`

// Options configure the server.
type Options struct {
	// CallTimeout is the longest a tool call blocks. Operations that take longer
	// return before it: a create keeps running in the background, a start reports
	// what is still pending. Zero picks it per client: no limit for clients that
	// wait for long calls, shortCallLimit for all others.
	CallTimeout time.Duration
}

// shortCallLimit keeps tool calls below the 60-second timeout of clients such as
// Codex, with room for the result to arrive.
const shortCallLimit = 50 * time.Second

// patientClients wait for tool calls of any length (by clientInfo name): Claude
// Code moves long calls to the background instead of cancelling them.
var patientClients = map[string]bool{"claude-code": true}

// callLimit is how long a call of client may block; 0 means no limit.
func callLimit(configured time.Duration, client *mcp.Implementation) time.Duration {
	switch {
	case configured > 0:
		return configured
	case client != nil && patientClients[client.Name]:
		return 0
	}
	return shortCallLimit
}

// within caps d at limit (0 means no limit).
func within(limit, d time.Duration) time.Duration {
	if limit > 0 {
		return min(d, limit)
	}
	return d
}

// New returns the server. log receives the progress of all operations as well
// (e.g. for the client's server log on stderr).
func New(version string, log progress.Sink, opts Options) *mcp.Server {
	if log == nil {
		log = progress.Discard
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "proxbase", Title: "Proxbase", Version: version},
		&mcp.ServerOptions{Instructions: instructions})
	h := &handlers{log: log, callTimeout: opts.CallTimeout}
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
func Run(ctx context.Context, version string, log progress.Sink, opts Options) error {
	return New(version, log, opts).Run(ctx, &mcp.StdioTransport{})
}

type handlers struct {
	log         progress.Sink
	callTimeout time.Duration // Options.CallTimeout
}

// limit is how long the call req may block; 0 means no limit.
func (h *handlers) limit(req *mcp.CallToolRequest) time.Duration {
	return callLimit(h.callTimeout, req.ClientInfo())
}

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
