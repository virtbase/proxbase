package mcpserver

import (
	"strings"
	"testing"
	"time"

	"github.com/virtbase/proxbase/internal/pve"
)

func TestNewTaskEntry(t *testing.T) {
	now := time.Unix(1000, 0)
	tests := []struct {
		name string
		task pve.Task
		want taskEntry
	}{
		{
			name: "ended",
			task: pve.Task{UPID: "u", Node: "pve1", Type: "qmstart", ID: "100", User: "root@pam", Status: "OK", StartTime: 900, EndTime: 930},
			want: taskEntry{UPID: "u", Node: "pve1", Type: "qmstart", ID: "100", User: "root@pam", Status: "OK",
				Started: "1970-01-01T00:15:00Z", Ended: "1970-01-01T00:15:30Z", Seconds: 30},
		},
		{
			name: "running",
			task: pve.Task{UPID: "u", Node: "pve2", Type: "vzdump", User: "root@pam", Status: "RUNNING", StartTime: 940},
			want: taskEntry{UPID: "u", Node: "pve2", Type: "vzdump", User: "root@pam", Status: "running", Started: "1970-01-01T00:15:40Z", Seconds: 60},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newTaskEntry(tt.task, now); got != tt.want {
				t.Errorf("newTaskEntry = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestTaskToolsCheckInput(t *testing.T) {
	cs, _ := connect(t)
	for _, tt := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"task_log", map[string]any{"upid": "100"}, "is not a task ID"},
		{"task_wait", map[string]any{"upid": "UPID:"}, "is not a task ID"},
		{"task_log", map[string]any{"cluster": "lab", "upid": upidOK}, "does not exist"},
		{"task_list", map[string]any{"cluster": "lab"}, "does not exist"},
	} {
		res, _ := call(t, cs, tt.tool, tt.args)
		if !res.IsError || !strings.Contains(text(res), tt.want) {
			t.Errorf("%s %v: %s, want an error with %q", tt.tool, tt.args, text(res), tt.want)
		}
	}
}
