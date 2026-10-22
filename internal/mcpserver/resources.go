package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/job"
	"github.com/virtbase/proxbase/internal/state"
)

// URIs of the resource templates.
const (
	statusURI = "proxbase://clusters/{cluster}/status"
	logURI    = "proxbase://clusters/{cluster}/logs/{file}"
	jobLogURI = "proxbase://jobs/{cluster}/log"
)

// maxResourceBytes is how much of the end of a log a resource returns.
const maxResourceBytes = 256 << 10

func (h *handlers) resources(s *mcp.Server) {
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: statusURI, Name: "cluster-status", Title: "Cluster status", MIMEType: "application/json",
		Description: "Phase, quorum, nodes, Ceph health, faults, owner and expiry of a cluster, as cluster_status returns them",
	}, h.readStatus)
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: logURI, Name: "cluster-log", Title: "Cluster log on the host", MIMEType: "text/plain",
		Description: "The end of a log file of a cluster on this host: <node>-console.log (serial console), <node>-install.log, <node>-qemu.log, switch.log",
	}, h.readLog)
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: jobLogURI, Name: "job-log", Title: "Background job log", MIMEType: "application/jsonl",
		Description: "The progress events (JSON lines) of the latest create or node change of a cluster",
	}, h.readJobLog)
}

func (h *handlers) readStatus(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	name, _, err := parseClusterURI(req.Params.URI, "proxbase://clusters/", "/status")
	if err != nil {
		return nil, err
	}
	c, err := cluster.Open(name, nil)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	s := c.Status(ctx)
	b, err := json.Marshal(statusOut{Status: s, Problem: s.Problem(), Healthy: s.Problem() == ""})
	if err != nil {
		return nil, err
	}
	return textResource(req.Params.URI, "application/json", string(b)), nil
}

func (h *handlers) readLog(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	name, file, err := parseClusterURI(req.Params.URI, "proxbase://clusters/", "")
	if err != nil {
		return nil, err
	}
	file, ok := strings.CutPrefix(file, "/logs/")
	if !ok || file == "" || strings.ContainsAny(file, `/\`) || strings.HasPrefix(file, ".") {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	text, err := tailText(state.ForCluster(name).Log(file))
	if err != nil {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	return textResource(req.Params.URI, "text/plain", text), nil
}

func (h *handlers) readJobLog(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	name, _, err := parseClusterURI(req.Params.URI, "proxbase://jobs/", "/log")
	if err != nil {
		return nil, err
	}
	j, err := job.New(name)
	if err != nil {
		return nil, err
	}
	text, err := tailText(j.Log())
	if err != nil {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	return textResource(req.Params.URI, "application/jsonl", text), nil
}

// parseClusterURI splits prefix<cluster><rest> and checks the cluster name; a
// non-empty suffix must be all of rest.
func parseClusterURI(uri, prefix, suffix string) (name, rest string, err error) {
	s, ok := strings.CutPrefix(uri, prefix)
	if !ok {
		return "", "", mcp.ResourceNotFoundError(uri)
	}
	name, rest, _ = strings.Cut(s, "/")
	if rest != "" {
		rest = "/" + rest
	}
	if config.CheckName(name) != nil || (suffix != "" && rest != suffix) {
		return "", "", mcp.ResourceNotFoundError(uri)
	}
	return name, rest, nil
}

// tailText returns the end of a file, at most maxResourceBytes.
func tailText(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > maxResourceBytes {
		if _, err := f.Seek(-maxResourceBytes, io.SeekEnd); err != nil {
			return "", err
		}
	}
	b, err := io.ReadAll(f)
	return string(b), err
}

func textResource(uri, mime, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: mime, Text: text}}}
}

// logLinks links the host logs of a cluster as resources.
func logLinks(clusterName string, paths []string) []mcp.Content {
	var links []mcp.Content
	for _, p := range paths {
		file := filepath.Base(p)
		links = append(links, &mcp.ResourceLink{
			URI: "proxbase://clusters/" + clusterName + "/logs/" + file, Name: file, MIMEType: "text/plain",
			Description: "host log " + p,
		})
	}
	return links
}

// complete suggests cluster names, nodes and log files for prompt and resource arguments.
func complete(_ context.Context, req *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	arg := req.Params.Argument
	var values []string
	switch arg.Name {
	case "cluster":
		values, _ = state.List()
	case "node", "file":
		var clusterName string
		if req.Params.Context != nil {
			clusterName = req.Params.Context.Arguments["cluster"]
		}
		if config.CheckName(clusterName) != nil {
			break
		}
		if arg.Name == "node" {
			if st, err := state.ForCluster(clusterName).Load(); err == nil {
				for _, n := range st.Nodes {
					values = append(values, n.Name)
				}
			}
		} else if entries, err := os.ReadDir(state.ForCluster(clusterName).Path("logs")); err == nil {
			for _, e := range entries {
				values = append(values, e.Name())
			}
		}
	}
	values = slices.DeleteFunc(values, func(v string) bool { return !strings.HasPrefix(v, arg.Value) })
	total := len(values)
	values = values[:min(total, 100)]
	return &mcp.CompleteResult{Completion: mcp.CompletionResultDetails{Values: nonNil(values), Total: total, HasMore: total > len(values)}}, nil
}
