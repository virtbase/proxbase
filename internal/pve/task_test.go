package pve

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTaskNode(t *testing.T) {
	tests := []struct {
		name, upid, node string
		ok               bool
	}{
		{name: "task", upid: "UPID:pve2:0000F0A1:0012A3B4:65000000:qmstart:100:root@pam:", node: "pve2", ok: true},
		{name: "not a upid", upid: "100"},
		{name: "empty node", upid: "UPID::1:2"},
		{name: "too short", upid: "UPID:pve1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, ok := TaskNode(tt.upid)
			if node != tt.node || ok != tt.ok {
				t.Errorf("TaskNode(%q) = %q, %v; want %q, %v", tt.upid, node, ok, tt.node, tt.ok)
			}
		})
	}
}

func TestTaskStatusOK(t *testing.T) {
	tests := []struct {
		exit string
		ok   bool
	}{
		{"OK", true},
		{"WARNINGS: 2", true},
		{"command failed", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.exit, func(t *testing.T) {
			if got := (TaskStatus{Status: "stopped", ExitStatus: tt.exit}).OK(); got != tt.ok {
				t.Errorf("OK() for %q = %v, want %v", tt.exit, got, tt.ok)
			}
		})
	}
}

// testClient returns a token client for a TLS test server running handler.
func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(p, fingerprintDER(srv.Certificate().Raw))
	c.SetToken("proxbase@pve!api=secret")
	return c
}

func TestTaskLogTail(t *testing.T) {
	const upid = "UPID:pve1:0001:000A:65000000:vzdump:100:root@pam:"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/nodes/pve1/tasks/"+upid+"/log" {
			http.NotFound(w, r)
			return
		}
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		var lines []string
		for n := start; n < min(start+limit, 120); n++ {
			lines = append(lines, fmt.Sprintf(`{"n":%d,"t":"line %d"}`, n+1, n+1))
		}
		fmt.Fprintf(w, `{"data":[%s],"total":120}`, strings.Join(lines, ","))
	})
	got, total, err := c.TaskLogTail(context.Background(), "pve1", upid, 3)
	if err != nil {
		t.Fatal(err)
	}
	if total != 120 || !slices.Equal(got, []string{"line 118", "line 119", "line 120"}) {
		t.Errorf("TaskLogTail = %q, %d; want the last 3 of 120", got, total)
	}
}

func TestTasks(t *testing.T) {
	var query url.Values
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		fmt.Fprint(w, `{"data":[{"upid":"UPID:pve2:1:2:3:qmstart:100:root@pam:","node":"pve2","type":"qmstart","id":"100",`+
			`"user":"root@pam","status":"RUNNING","starttime":100,"saved":"0"}]}`)
	})
	since := time.Unix(1700000000, 0)
	tasks, err := c.Tasks(context.Background(), "pve2", TaskFilter{Limit: 5, Errors: true, Type: "qmstart", VMID: 100, Since: since})
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{"source": {"all"}, "limit": {"5"}, "errors": {"1"}, "typefilter": {"qmstart"}, "vmid": {"100"}, "since": {"1700000000"}}
	if query.Encode() != want.Encode() {
		t.Errorf("query = %s, want %s", query.Encode(), want.Encode())
	}
	if len(tasks) != 1 || !tasks[0].Running() || tasks[0].Failed() {
		t.Errorf("tasks = %+v; want one running task", tasks)
	}
	if _, err := c.Tasks(context.Background(), "pve2", TaskFilter{Running: true}); err != nil || query.Get("source") != "active" {
		t.Errorf("running tasks: source=%q, %v", query.Get("source"), err)
	}
}

func TestTaskFailed(t *testing.T) {
	tests := []struct {
		name string
		task Task
		want bool
	}{
		{name: "running", task: Task{Status: "RUNNING"}, want: false},
		{name: "ok", task: Task{Status: "OK", EndTime: 2}, want: false},
		{name: "warnings", task: Task{Status: "WARNINGS: 1", EndTime: 2}, want: false},
		{name: "error", task: Task{Status: "job errors", EndTime: 2}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.task.Failed(); got != tt.want {
				t.Errorf("Failed() = %v, want %v", got, tt.want)
			}
		})
	}
}
