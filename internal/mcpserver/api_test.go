package mcpserver

import (
	"context"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/virtbase/proxbase/internal/pve"
)

const (
	upidOK   = "UPID:pve1:00001:0000A:65000000:qmcreate:100:proxbase@pve!api:"
	upidFail = "UPID:pve1:00002:0000B:65000001:qmstart:101:proxbase@pve!api:"
)

// fakePVE serves a few API paths the way Proxmox VE does. The create task runs
// for one status poll, the start task fails.
func fakePVE(t *testing.T) *pve.Client {
	t.Helper()
	var polls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken=proxbase@pve!api=secret" {
			http.Error(w, `{"message":"no ticket"}`, http.StatusUnauthorized)
			return
		}
		task := strings.TrimPrefix(r.URL.Path, "/api2/json/nodes/pve1/tasks/")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api2/json/cluster/resources":
			if r.URL.Query().Get("type") != "vm" {
				http.Error(w, `{"message":"type missing"}`, http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, `{"data":[{"vmid":100,"type":"vm"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api2/json/nodes/pve1/qemu":
			if err := r.ParseForm(); err != nil || r.PostForm.Get("vmid") != "100" || r.PostForm.Get("onboot") != "1" {
				http.Error(w, `{"message":"parameter verification failed","errors":{"vmid":"missing"}}`, http.StatusBadRequest)
				return
			}
			fmt.Fprintf(w, `{"data":%q}`, upidOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api2/json/nodes/pve1/qemu/101/status/start":
			fmt.Fprintf(w, `{"data":%q}`, upidFail)
		case task == upidOK+"/status":
			if polls.Add(1) == 1 {
				fmt.Fprint(w, `{"data":{"status":"running"}}`)
				return
			}
			fmt.Fprint(w, `{"data":{"status":"stopped","exitstatus":"OK"}}`)
		case task == upidFail+"/status":
			fmt.Fprint(w, `{"data":{"status":"stopped","exitstatus":"start failed: boom"}}`)
		case strings.HasSuffix(task, "/log"):
			fmt.Fprint(w, `{"data":[{"n":1,"t":"first line"},{"n":2,"t":"last line"}]}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api2/json/pools/lab":
			fmt.Fprint(w, `{"data":null}`)
		default:
			http.Error(w, `{"message":"no such path"}`, http.StatusNotImplemented)
		}
	}))
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	fp, err := pve.Fingerprint(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})))
	if err != nil {
		t.Fatal(err)
	}
	api := pve.NewClient(p, fp)
	api.SetToken("proxbase@pve!api=secret")
	return api
}

func TestCallAPI(t *testing.T) {
	api := fakePVE(t)
	ctx := context.Background()

	data, err := callAPI(ctx, api, http.MethodGet, "/cluster/resources", url.Values{"type": {"vm"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(data); got != "[map[type:vm vmid:100]]" {
		t.Errorf("GET data = %s", got)
	}
	data, err = callAPI(ctx, api, http.MethodPost, "/nodes/pve1/qemu", url.Values{"vmid": {"100"}, "onboot": {"1"}})
	if err != nil || data != upidOK {
		t.Errorf("POST = %v, %v; want the UPID", data, err)
	}
	if data, err = callAPI(ctx, api, http.MethodDelete, "/pools/lab", nil); err != nil || data != nil {
		t.Errorf("DELETE = %v, %v; want null data", data, err)
	}
	_, err = callAPI(ctx, api, http.MethodPost, "/nodes/pve1/qemu", url.Values{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400: parameter verification failed; vmid: missing") {
		t.Errorf("POST without parameters: %v", err)
	}
	if _, err = callAPI(ctx, api, http.MethodGet, "/nope", nil); err == nil || !strings.Contains(err.Error(), "GET /nope: HTTP 501") {
		t.Errorf("GET of an unknown path: %v", err)
	}
}

func TestWaitTask(t *testing.T) {
	api := fakePVE(t)
	ctx := context.Background()

	task, err := waitTask(ctx, api, "pve1", upidOK, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "stopped" || task.ExitStatus != "OK" || !slices.Equal(task.Log, []string{"first line", "last line"}) {
		t.Errorf("task = %+v", task)
	}
	_, err = waitTask(ctx, api, "pve1", upidFail, time.Now().Add(30*time.Second))
	if err == nil || !strings.Contains(err.Error(), "failed: start failed: boom\nfirst line\nlast line") {
		t.Errorf("failed task: %v", err)
	}
}

func TestWaitTaskDeadline(t *testing.T) {
	api := fakePVE(t)
	task, err := waitTask(context.Background(), api, "pve1", upidOK, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "running" {
		t.Errorf("task after the deadline = %+v, want running", task)
	}
}

func TestAPIPath(t *testing.T) {
	tests := []struct {
		name, path, want, err string
	}{
		{name: "plain", path: "/cluster/resources", want: "/cluster/resources"},
		{name: "with prefix", path: "/api2/json/nodes/pve1/qemu", want: "/nodes/pve1/qemu"},
		{name: "upid", path: "/nodes/pve1/tasks/" + upidOK + "/log", want: "/nodes/pve1/tasks/" + upidOK + "/log"},
		{name: "relative", path: "cluster/resources", err: "must start with /"},
		{name: "query", path: "/cluster/resources?type=vm", err: "put parameters in params"},
		{name: "dot segment", path: "/nodes/../../pve2", err: "must not contain"},
		{name: "encoded dot segment", path: "/nodes/%2e%2e/x", err: "must not contain"},
		{name: "space", path: "/nodes/pve1 /qemu", err: "spaces"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := apiPath(tt.path)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Errorf("apiPath(%q) error = %v, want %q", tt.path, err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("apiPath(%q) = %q, %v; want %q", tt.path, got, err, tt.want)
			}
		})
	}
}

func TestFormValues(t *testing.T) {
	got, err := formValues(map[string]any{
		"vmid": float64(100), "memory": "2048", "onboot": true, "template": false,
		"cpulimit": 1.5, "delete": []any{"net1", "net2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{"vmid": {"100"}, "memory": {"2048"}, "onboot": {"1"}, "template": {"0"}, "cpulimit": {"1.5"}, "delete": {"net1", "net2"}}
	if got.Encode() != want.Encode() {
		t.Errorf("formValues = %s, want %s", got.Encode(), want.Encode())
	}
	for _, bad := range []map[string]any{{"net0": map[string]any{"bridge": "vmbr0"}}, {"x": nil}, {"list": []any{[]any{"a"}}}} {
		if _, err := formValues(bad); err == nil {
			t.Errorf("formValues(%v) accepted it", bad)
		}
	}
}

func TestAPIToolsCheckInput(t *testing.T) {
	cs, _ := connect(t)
	for _, tt := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"pve_call", map[string]any{"method": "GET", "path": "/version"}, "use pve_get"},
		{"pve_call", map[string]any{"method": "PATCH", "path": "/version"}, "must be POST, PUT or DELETE"},
		{"pve_get", map[string]any{"path": "/../x"}, "must not contain"},
		{"pve_get", map[string]any{"path": "/version", "params": map[string]any{"x": map[string]any{}}}, "params.x"},
		{"pve_get", map[string]any{"cluster": "lab", "path": "/version"}, "does not exist"},
	} {
		res, _ := call(t, cs, tt.tool, tt.args)
		if !res.IsError || !strings.Contains(text(res), tt.want) {
			t.Errorf("%s %v: %s, want an error with %q", tt.tool, tt.args, text(res), tt.want)
		}
	}
}
