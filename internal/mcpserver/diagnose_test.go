package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDiagnoseScript(t *testing.T) {
	s := diagnoseScript(15*time.Minute, false)
	for _, want := range []string{"--since '-15min'", "-u 'corosync'", "-u 'ceph*'", "tail -n 400", "### boot", "### clock"} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "ceph health") {
		t.Errorf("script without Ceph asks for ceph health")
	}
	if !strings.Contains(diagnoseScript(time.Hour, true), "ceph health detail") {
		t.Errorf("script with Ceph does not ask for ceph health")
	}
}

func TestParseReport(t *testing.T) {
	now := time.Unix(0x65000100, 0)
	output := strings.Join([]string{
		"### boot",
		"1792663200", // 2026-10-22T10:00:00Z
		"### quorum",
		"Quorate:          Yes",
		"Total votes:      2",
		"### failed-units",
		"pve-ha-lrm.service",
		"### failed-tasks",
		"UPID:pve1:0001:000A:65000000:qmstart:100:root@pam: 65000010 start failed: QEMU exited with code 1",
		"UPID:pve1:0002:000B:65000020:vzdump:101:root@pam: 65000080 job errors",
		"UPID:pve1:0003:000C:60000000:qmigrate:102:root@pam: 60000010 migration aborted",
		"not a task line",
		"### errors",
		"2026-10-22T10:00:20+00:00 pve1 pmxcfs[99]: [quorum] crit: quorum_initialize failed: CS_ERR_LIBRARY",
		"2026-10-22T10:01:50+00:00 pve1 pmxcfs[99]: [dcdb] crit: cpg_initialize failed: CS_ERR_LIBRARY",
		"2026-10-22T10:30:00+00:00 pve1 corosync[123]: [KNET] link: host: 2 link: 0 is down",
		"",
		"### clock",
		"0.000012 seconds fast of NTP time",
		"### ceph",
		"HEALTH_WARN 1 osds down",
	}, "\n")
	got := parseReport("pve1", output, now.Add(-time.Hour))
	if got.Name != "pve1" || !slices.Equal(got.Quorum, []string{"Quorate: Yes", "Total votes: 2"}) {
		t.Errorf("quorum = %q", got.Quorum)
	}
	if !slices.Equal(got.FailedUnits, []string{"pve-ha-lrm.service"}) {
		t.Errorf("failed units = %q", got.FailedUnits)
	}
	if len(got.FailedTasks) != 2 || got.FailedTasks[0].Type != "vzdump" || got.FailedTasks[1].Status != "start failed: QEMU exited with code 1" {
		t.Errorf("failed tasks (newest first, old one left out) = %+v", got.FailedTasks)
	}
	if len(got.Errors) != 1 || !strings.Contains(got.Errors[0], "is down") || got.Startup != 2 {
		t.Errorf("errors = %q, startup errors = %d; want the link error and 2 left out", got.Errors, got.Startup)
	}
	if got.Clock != "0.000012 seconds fast of NTP time" || !slices.Equal(got.Ceph, []string{"HEALTH_WARN 1 osds down"}) {
		t.Errorf("clock = %q, ceph = %q", got.Clock, got.Ceph)
	}
}

func TestParseTask(t *testing.T) {
	tests := []struct {
		name, line string
		want       failedTask
		ok         bool
	}{
		{
			name: "failed start",
			line: "UPID:pve2:0001:000A:65000000:qmstart:100:root@pam: 65000010 start failed: boom",
			want: failedTask{UPID: "UPID:pve2:0001:000A:65000000:qmstart:100:root@pam:", Type: "qmstart", ID: "100",
				Ended: time.Unix(0x65000010, 0).UTC().Format(time.RFC3339), Status: "start failed: boom"},
			ok: true,
		},
		{name: "no status", line: "UPID:pve2:0001:000A:65000000:qmstart:100:root@pam: 65000010"},
		{name: "bad end time", line: "UPID:pve2:0001:000A:65000000:qmstart:100:root@pam: zz failed"},
		{name: "not a upid", line: "hello world again"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseTask(tt.line)
			if ok != tt.ok || got.failedTask != tt.want {
				t.Errorf("parseTask(%q) = %+v, %v; want %+v, %v", tt.line, got.failedTask, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestTailFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.log")
	var b strings.Builder
	for i := range 2000 {
		fmt.Fprintf(&b, "line %d\n\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := tailFile(path, 2); !slices.Equal(got, []string{"line 1998", "line 1999"}) {
		t.Errorf("tailFile = %q", got)
	}
	if got := tailFile(filepath.Join(t.TempDir(), "missing"), 2); got != nil {
		t.Errorf("tailFile of a missing file = %q", got)
	}
}
