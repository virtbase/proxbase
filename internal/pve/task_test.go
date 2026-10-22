package pve

import "testing"

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
