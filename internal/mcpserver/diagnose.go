package mcpserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/cluster"
)

const diagnoseDescription = `Collect what explains a cluster problem, in one read-only call: the cluster status and, for every node,
the quorum seen by it, failed systemd units, PVE tasks that failed, error log lines of corosync, pmxcfs (pve-cluster),
pveproxy, pvedaemon, pvestatd, HA and Ceph from the last sinceMinutes, the clock offset, and ceph health detail.
For nodes that are not running it returns the end of their serial console. Also lists the log files on this host.`

type diagnoseIn struct {
	Cluster      string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	SinceMinutes int    `json:"sinceMinutes,omitempty" jsonschema:"report errors and failed tasks of the last this many minutes (default 30, max 1440)"`
}

type diagnoseOut struct {
	Healthy bool            `json:"healthy"`
	Problem string          `json:"problem,omitempty"`
	Status  *cluster.Status `json:"status"`
	Nodes   []nodeReport    `json:"nodes"`
	Logs    []string        `json:"logs" jsonschema:"log files on this host: QEMU, install and serial console of each node, the switch"`
}

type nodeReport struct {
	Name        string       `json:"name"`
	Error       string       `json:"error,omitempty" jsonschema:"why the node could not be inspected"`
	Quorum      []string     `json:"quorum,omitempty" jsonschema:"from pvecm status"`
	FailedUnits []string     `json:"failedUnits,omitempty" jsonschema:"systemd units in the failed state"`
	FailedTasks []failedTask `json:"failedTasks,omitempty" jsonschema:"tasks that ended with an error in the period, newest first; pve_get /nodes/<node>/tasks/<upid>/log shows a log"`
	Errors      []string     `json:"errors,omitempty" jsonschema:"error log lines of the cluster services in the period, oldest first"`
	Clock       string       `json:"clock,omitempty" jsonschema:"offset of the system clock from NTP time (chrony)"`
	Ceph        []string     `json:"ceph,omitempty" jsonschema:"ceph health detail"`
	Console     []string     `json:"console,omitempty" jsonschema:"end of the serial console of a node that is not running"`
}

type failedTask struct {
	UPID   string `json:"upid"`
	Type   string `json:"type" jsonschema:"e.g. qmstart, vzdump, qmigrate"`
	ID     string `json:"id,omitempty" jsonschema:"guest or object the task worked on"`
	Ended  string `json:"ended" jsonschema:"RFC 3339"`
	Status string `json:"status"`
}

const (
	// diagnoseTimeout bounds the inspection of the nodes, which run in parallel.
	diagnoseTimeout = 30 * time.Second
	// maxErrors and maxFailedTasks are the most lines and tasks per node.
	maxErrors      = 40
	maxFailedTasks = 10
	// maxLine cuts long log lines.
	maxLine = 300
	// consoleLines is the end of the console of a stopped node.
	consoleLines = 20
)

// diagnoseUnits are the services whose errors explain most cluster problems.
var diagnoseUnits = []string{"corosync", "pve-cluster", "pveproxy", "pvedaemon", "pvestatd", "pve-ha-lrm", "pve-ha-crm", "ceph*"}

// diagnoseScript prints one section per finding, each after a "### name" line.
func diagnoseScript(since time.Duration, ceph bool) string {
	var b strings.Builder
	b.WriteString("echo '### quorum'; pvecm status 2>&1 | grep -E '^(Quorate|Expected votes|Total votes|Quorum):'\n")
	b.WriteString("echo '### failed-units'; systemctl --failed --no-legend --plain 2>/dev/null | awk '{print $1}'\n")
	b.WriteString("echo '### failed-tasks'; grep -v -e ' OK$' -e ' WARNINGS: ' /var/log/pve/tasks/index 2>/dev/null\n")
	fmt.Fprintf(&b, "echo '### errors'; journalctl -q -p err --since '-%dmin' -o short-iso --no-pager", int(since.Minutes()))
	for _, u := range diagnoseUnits {
		fmt.Fprintf(&b, " -u '%s'", u)
	}
	fmt.Fprintf(&b, " 2>/dev/null | tail -n %d\n", maxErrors)
	b.WriteString("echo '### clock'; chronyc tracking 2>/dev/null | sed -n 's/^System time *: //p'\n")
	if ceph {
		b.WriteString("echo '### ceph'; timeout 10 ceph health detail 2>&1 | head -n 30\n")
	}
	return b.String()
}

func (h *handlers) diagnose(ctx context.Context, req *mcp.CallToolRequest, in diagnoseIn) (*mcp.CallToolResult, diagnoseOut, error) {
	c, err := h.open(in.Cluster, nil)
	if err != nil {
		return nil, diagnoseOut{}, err
	}
	since := time.Duration(min(max(in.SinceMinutes, 0), 1440)) * time.Minute
	if since == 0 {
		since = 30 * time.Minute
	}
	s := c.Status(ctx)
	out := diagnoseOut{Status: s, Problem: s.Problem(), Logs: hostLogs(c), Nodes: make([]nodeReport, len(s.Nodes))}
	out.Healthy = out.Problem == ""

	nctx, cancel := context.WithTimeout(ctx, within(beforeStatus(h.limit(req)), diagnoseTimeout))
	defer cancel()
	ceph := c.Cfg.CephEnabled()
	var wg sync.WaitGroup
	for i, n := range s.Nodes {
		if !n.Running {
			out.Nodes[i] = nodeReport{Name: n.Name, Error: "not running", Console: tailFile(c.Dir.Log(n.Name+"-console.log"), consoleLines)}
			continue
		}
		script := diagnoseScript(since, ceph)
		ceph = false // once is enough
		wg.Go(func() {
			r, err := c.Exec(nctx, n.Name, script)
			if err != nil {
				out.Nodes[i] = nodeReport{Name: n.Name, Error: err.Error()}
				return
			}
			out.Nodes[i] = parseReport(n.Name, r.Stdout, time.Now().Add(-since))
		})
	}
	wg.Wait()
	return nil, out, nil
}

// parseReport reads the output of diagnoseScript; tasks that ended before
// since are left out.
func parseReport(node, output string, since time.Time) nodeReport {
	r := nodeReport{Name: node}
	sections := map[string][]string{}
	var cur string
	for line := range strings.Lines(output) {
		line = strings.TrimRight(line, "\r\n")
		if name, ok := strings.CutPrefix(line, "### "); ok {
			cur = name
			continue
		}
		if strings.TrimSpace(line) != "" && cur != "" {
			sections[cur] = append(sections[cur], cut(line))
		}
	}
	for _, l := range sections["quorum"] {
		r.Quorum = append(r.Quorum, strings.Join(strings.Fields(l), " "))
	}
	r.FailedUnits = sections["failed-units"]
	r.Errors = sections["errors"]
	r.Ceph = sections["ceph"]
	if clock := sections["clock"]; len(clock) > 0 {
		r.Clock = clock[0]
	}
	for _, l := range sections["failed-tasks"] {
		if t, ok := parseTask(l); ok && t.ended.After(since) {
			r.FailedTasks = append(r.FailedTasks, t.failedTask)
		}
	}
	slices.SortStableFunc(r.FailedTasks, func(a, b failedTask) int { return strings.Compare(b.Ended, a.Ended) })
	r.FailedTasks = r.FailedTasks[:min(len(r.FailedTasks), maxFailedTasks)]
	return r
}

type parsedTask struct {
	failedTask
	ended time.Time
}

// parseTask reads a line of /var/log/pve/tasks/index: the UPID
// (UPID:node:pid:pstart:starttime:type:id:user:), the end time in hex and the status.
func parseTask(line string) (parsedTask, bool) {
	upid, rest, ok := strings.Cut(line, " ")
	if !ok {
		return parsedTask{}, false
	}
	endHex, status, ok := strings.Cut(rest, " ")
	if !ok {
		return parsedTask{}, false
	}
	f := strings.Split(upid, ":")
	end, err := strconv.ParseInt(endHex, 16, 64)
	if err != nil || len(f) < 8 || f[0] != "UPID" {
		return parsedTask{}, false
	}
	ended := time.Unix(end, 0).UTC()
	return parsedTask{
		failedTask: failedTask{UPID: upid, Type: f[5], ID: f[6], Ended: ended.Format(time.RFC3339), Status: status},
		ended:      ended,
	}, true
}

// cut shortens a line to maxLine bytes.
func cut(line string) string {
	if len(line) <= maxLine {
		return line
	}
	return line[:maxLine] + "…"
}

// hostLogs lists the cluster's log files on this host.
func hostLogs(c *cluster.Cluster) []string {
	logs := []string{}
	entries, err := os.ReadDir(c.Dir.Path("logs"))
	if err != nil {
		return logs
	}
	for _, e := range entries {
		if !e.IsDir() {
			logs = append(logs, c.Dir.Path("logs", e.Name()))
		}
	}
	return logs
}

// tailFile returns the last n non-empty lines of a file, reading at most its last 16 KiB.
func tailFile(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	const window = 16 << 10
	if fi, err := f.Stat(); err == nil && fi.Size() > window {
		if _, err := f.Seek(-window, io.SeekEnd); err != nil {
			return nil
		}
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	var lines []string
	for line := range bytes.Lines(b) {
		if l := strings.TrimSpace(string(line)); l != "" {
			lines = append(lines, cut(l))
		}
	}
	return lines[max(0, len(lines)-n):]
}
