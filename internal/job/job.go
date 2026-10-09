// Package job runs a proxbase command detached from the caller (for example a
// create started by an MCP client), with its progress as JSON lines in a file.
// A job survives the caller; one job per cluster can run at a time.
package job

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/progress"
	"github.com/virtbase/proxbase/internal/state"
)

// Job is the detached command of one cluster.
type Job struct{ cluster string }

// New returns the job slot of a cluster.
func New(cluster string) (Job, error) {
	if err := config.CheckName(cluster); err != nil {
		return Job{}, err
	}
	return Job{cluster: cluster}, nil
}

// Cluster is the name of the job's cluster.
func (j Job) Cluster() string { return j.cluster }

func dir() string { return filepath.Join(state.DataHome(), "jobs") }

// Log is the file with the job's progress events.
func (j Job) Log() string { return j.file(".jsonl") }

func (j Job) pidFile() string { return j.file(".pid") }

func (j Job) file(ext string) string { return filepath.Join(dir(), j.cluster+ext) }

// WriteInput stores the cluster file for the job and returns its path.
func (j Job) WriteInput(data []byte) (string, error) {
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return "", err
	}
	path := j.file(".yaml")
	return path, state.WriteFileAtomic(path, data, 0o644)
}

// Start runs `<this executable> args... --progress json` in its own session.
// exited is closed when the job ends, if the caller lives that long.
func (j Job) Start(args ...string) (exited <-chan struct{}, err error) {
	if pid := j.PID(); pid != 0 {
		return nil, fmt.Errorf("a job for cluster %s is already running (pid %d)", j.cluster, pid)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return nil, err
	}
	log, err := os.Create(j.Log())
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, append(args, "--progress", "json")...)
	cmd.Stderr = log // stdout (the final status table) is dropped; the log has a done/error event
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, err
	}
	if err := state.WriteFileAtomic(j.pidFile(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		_ = cmd.Process.Kill()
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait() // reap, so PID does not see a zombie
		log.Close()
		close(done)
	}()
	return done, err
}

// PID returns the process ID of the running job, or 0.
func (j Job) PID() int {
	b, err := os.ReadFile(j.pidFile())
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 || syscall.Kill(pid, 0) != nil {
		return 0
	}
	// Guard against PID reuse where /proc exists.
	if cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil && !bytes.Contains(cmdline, []byte("--progress\x00json")) {
		return 0
	}
	return pid
}

// Signal sends sig to the running job, if any.
func (j Job) Signal(sig syscall.Signal) error {
	if pid := j.PID(); pid != 0 {
		return syscall.Kill(pid, sig)
	}
	return nil
}

// Remove deletes the job's files.
func (j Job) Remove() {
	for _, p := range []string{j.Log(), j.pidFile(), j.file(".yaml")} {
		_ = os.Remove(p)
	}
}

// Events returns the events logged after byte offset from, and the new offset.
// A partially written last line is left for the next call.
func (j Job) Events(from int64) ([]progress.Event, int64, error) {
	f, err := os.Open(j.Log())
	if errors.Is(err, os.ErrNotExist) {
		return nil, from, nil
	}
	if err != nil {
		return nil, from, err
	}
	defer f.Close()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, from, err
	}
	var out []progress.Event
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return out, from, nil
		}
		from += int64(len(line))
		var e progress.Event
		if json.Unmarshal(line, &e) != nil {
			e = progress.Event{Type: progress.TypeLog, Message: strings.TrimSpace(string(line))}
		}
		out = append(out, e)
	}
}
