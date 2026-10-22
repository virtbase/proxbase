package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/pve"
)

type apiGetIn struct {
	Cluster string         `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Node    string         `json:"node,omitempty" jsonschema:"node whose API answers (default: the first running node); /nodes/<name>/... paths reach every node through it"`
	Path    string         `json:"path" jsonschema:"API path under /api2/json, e.g. /cluster/resources, /nodes/pve1/qemu or /nodes/pve1/tasks/<upid>/log"`
	Params  map[string]any `json:"params,omitempty" jsonschema:"query parameters, e.g. {\"type\": \"vm\"}"`
}

type apiCallIn struct {
	Cluster        string         `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Node           string         `json:"node,omitempty" jsonschema:"node whose API answers (default: the first running node); /nodes/<name>/... paths reach every node through it"`
	Method         string         `json:"method" jsonschema:"POST (create or run), PUT (change) or DELETE"`
	Path           string         `json:"path" jsonschema:"API path under /api2/json, e.g. /nodes/pve1/qemu or /nodes/pve1/qemu/100/status/start"`
	Params         map[string]any `json:"params,omitempty" jsonschema:"parameters, e.g. {\"vmid\": 100, \"memory\": 2048, \"net0\": \"virtio,bridge=vmbr0\"}; booleans become 1 or 0, arrays repeat the parameter"`
	TimeoutSeconds int            `json:"timeoutSeconds,omitempty" jsonschema:"wait this long for a task the call starts (default 120, max 3600; at most 50 for clients that cancel tool calls after a minute)"`
}

type apiOut struct {
	Data any      `json:"data" jsonschema:"the data of the API response"`
	Task *taskOut `json:"task,omitempty" jsonschema:"the task the call started"`
}

type taskOut struct {
	UPID       string   `json:"upid"`
	Node       string   `json:"node"`
	Status     string   `json:"status" jsonschema:"running or stopped; poll a running task with pve_get /nodes/<node>/tasks/<upid>/status"`
	ExitStatus string   `json:"exitStatus,omitempty" jsonschema:"OK or WARNINGS: n"`
	Log        []string `json:"log,omitempty" jsonschema:"last lines of the task log"`
}

const (
	// maxAPIResponse limits the data a call returns to the client.
	maxAPIResponse = 512 << 10
	// taskLogLines is how much of a task log results carry.
	taskLogLines = 30
	// apiRequestTimeout bounds one request that is not waiting for a task.
	apiRequestTimeout = 60 * time.Second
	// taskPollTimeout bounds each poll of a task; the wait ends this long before
	// the call's budget so the last poll and the log fit in it.
	taskPollTimeout = 5 * time.Second
)

const (
	pveGetDescription = `Read from the Proxmox VE API of a cluster with its API token (proxbase@pve!api) and return the response data as JSON.
Paths are those of pvesh and the API viewer, e.g. /cluster/resources, /cluster/status, /nodes/pve1/qemu, /nodes/pve1/storage,
/nodes/pve1/tasks (recent tasks) or /nodes/pve1/tasks/<upid>/log.`
	pveCallDescription = `Change something through the Proxmox VE API of a cluster with its API token: POST, PUT or DELETE a path with params,
e.g. POST /nodes/pve1/qemu {"vmid": 100, "memory": 2048} or POST /nodes/pve1/qemu/100/status/start.
If the call starts a task (returns a UPID), waits for it and returns its exit status and log; a failed task is an error.`
)

func (h *handlers) pveGet(ctx context.Context, req *mcp.CallToolRequest, in apiGetIn) (*mcp.CallToolResult, apiOut, error) {
	api, path, form, err := h.apiRequest(in.Cluster, in.Node, in.Path, in.Params)
	if err != nil {
		return nil, apiOut{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, within(h.limit(req), apiRequestTimeout))
	defer cancel()
	data, err := callAPI(ctx, api, http.MethodGet, path, form)
	return nil, apiOut{Data: data}, err
}

func (h *handlers) pveCall(ctx context.Context, req *mcp.CallToolRequest, in apiCallIn) (*mcp.CallToolResult, apiOut, error) {
	method := strings.ToUpper(in.Method)
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodDelete:
	case http.MethodGet:
		return nil, apiOut{}, errors.New("use pve_get for GET")
	default:
		return nil, apiOut{}, fmt.Errorf("method must be POST, PUT or DELETE, not %q", in.Method)
	}
	api, path, form, err := h.apiRequest(in.Cluster, in.Node, in.Path, in.Params)
	if err != nil {
		return nil, apiOut{}, err
	}
	deadline := time.Now().Add(within(h.limit(req), seconds(in.TimeoutSeconds, 120, 3600)))
	rctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	data, err := callAPI(rctx, api, method, path, form)
	if err != nil {
		return nil, apiOut{}, err
	}
	out := apiOut{Data: data}
	upid, _ := data.(string)
	node, ok := pve.TaskNode(upid)
	if !ok {
		return nil, out, nil
	}
	if out.Task, err = waitTask(ctx, api, node, upid, deadline.Add(-taskPollTimeout)); err != nil {
		return nil, apiOut{}, err
	}
	return nil, out, nil
}

// apiRequest returns the client and the checked path and parameters of a call.
func (h *handlers) apiRequest(clusterName, node, path string, params map[string]any) (*pve.Client, string, url.Values, error) {
	path, err := apiPath(path)
	if err != nil {
		return nil, "", nil, err
	}
	form, err := formValues(params)
	if err != nil {
		return nil, "", nil, err
	}
	c, err := h.open(clusterName, nil)
	if err != nil {
		return nil, "", nil, err
	}
	api, err := c.TokenAPI(node)
	if err != nil {
		return nil, "", nil, err
	}
	return api, path, form, nil
}

// callAPI sends a request and returns the decoded data of the response. GET and
// DELETE send the parameters in the query, POST and PUT in the body.
func callAPI(ctx context.Context, api *pve.Client, method, path string, form url.Values) (any, error) {
	if (method == http.MethodGet || method == http.MethodDelete) && len(form) > 0 {
		path += "?" + form.Encode()
		form = nil
	}
	if len(form) == 0 {
		form = nil
	}
	var raw json.RawMessage
	if err := api.Do(ctx, method, path, form, &raw); err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	if len(raw) > maxAPIResponse {
		return nil, fmt.Errorf("%s %s: the response has %d KiB, more than %d KiB; narrow it with a more specific path or parameters",
			method, path, len(raw)>>10, maxAPIResponse>>10)
	}
	if raw == nil {
		return nil, nil
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	return data, nil
}

// waitTask polls a task until it stops or until deadline, and returns its state
// with the end of its log. A task that failed is an error.
func waitTask(ctx context.Context, api *pve.Client, node, upid string, deadline time.Time) (*taskOut, error) {
	var st pve.TaskStatus
	for {
		pctx, cancel := context.WithTimeout(ctx, taskPollTimeout)
		cur, err := api.TaskStatus(pctx, node, upid)
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			st = cur
		}
		if st.Stopped() {
			break
		}
		if time.Now().After(deadline) {
			if st.Status == "" {
				return nil, fmt.Errorf("task %s: %w", upid, err)
			}
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	out := &taskOut{UPID: upid, Node: node, Status: st.Status, ExitStatus: st.ExitStatus}
	lctx, cancel := context.WithTimeout(ctx, taskPollTimeout)
	defer cancel()
	if log, _, err := api.TaskLogTail(lctx, node, upid, taskLogLines); err == nil {
		out.Log = log
	}
	if st.Stopped() && !st.OK() {
		return nil, fmt.Errorf("task %s failed: %s\n%s", upid, st.ExitStatus, strings.Join(out.Log, "\n"))
	}
	return out, nil
}

// apiPath checks an API path from a client: absolute, below /api2/json, with the
// parameters apart and without dot segments.
func apiPath(p string) (string, error) {
	p = strings.TrimPrefix(p, "/api2/json")
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("path %q must start with /, e.g. /cluster/resources", p)
	}
	if strings.ContainsAny(p, "?#") {
		return "", errors.New("put parameters in params, not in the path")
	}
	if strings.ContainsFunc(p, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return "", fmt.Errorf("path %q contains spaces or control characters", p)
	}
	decoded, err := url.PathUnescape(p)
	if err != nil {
		return "", fmt.Errorf("path %q: %w", p, err)
	}
	for _, seg := range strings.Split(decoded, "/") {
		if seg == "." || seg == ".." {
			return "", fmt.Errorf("path %q must not contain . or .. segments", p)
		}
	}
	return p, nil
}

// formValues turns JSON parameters into API parameters: strings as they are,
// numbers in decimal, booleans as 1 or 0, arrays as repeated parameters.
func formValues(params map[string]any) (url.Values, error) {
	form := url.Values{}
	for k, v := range params {
		values, ok := v.([]any)
		if !ok {
			values = []any{v}
		}
		for _, e := range values {
			s, err := formValue(e)
			if err != nil {
				return nil, fmt.Errorf("params.%s: %w", k, err)
			}
			form.Add(k, s)
		}
	}
	return form, nil
}

func formValue(v any) (string, error) {
	switch v := v.(type) {
	case string:
		return v, nil
	case bool:
		if v {
			return "1", nil
		}
		return "0", nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case json.Number:
		return v.String(), nil
	default:
		return "", fmt.Errorf("unsupported value %v (%T); use a string, number, boolean or an array of them", v, v)
	}
}
