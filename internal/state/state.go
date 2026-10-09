// Package state owns the on-disk layout of a cluster:
// $XDG_DATA_HOME/proxbase/clusters/<name>/{cluster.yaml,state.json,secrets/,disks/,run/,logs/}.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/virtbase/proxbase/internal/config"
)

type Phase string

const (
	PhaseCreating Phase = "creating"
	PhaseReady    Phase = "ready"
	PhaseFailed   Phase = "failed"
)

type State struct {
	Name      string       `json:"name"`
	Phase     Phase        `json:"phase"`
	Error     string       `json:"error,omitempty"`
	ISO       string       `json:"iso,omitempty"`
	CreatedAt time.Time    `json:"createdAt"`
	ReadyAt   *time.Time   `json:"readyAt,omitempty"`
	Duration  string       `json:"createDuration,omitempty"`
	Nodes     []*NodeState `json:"nodes"`
}

type NodeState struct {
	Name      string `json:"name"`
	Index     int    `json:"index"`
	Installed bool   `json:"installed"`
	HostKey   string `json:"hostKey,omitempty"` // SSH host key, trusted on first use
	UIPort    int    `json:"uiPort"`
	SSHPort   int    `json:"sshPort"`
}

func (s *State) Node(name string) *NodeState {
	for _, n := range s.Nodes {
		if n.Name == name {
			return n
		}
	}
	return nil
}

func DataHome() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "proxbase")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "share", "proxbase")
}

func CacheHome() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return filepath.Join(d, "proxbase")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".cache", "proxbase")
}

func ClustersDir() string { return filepath.Join(DataHome(), "clusters") }

// Dir is a cluster directory.
type Dir string

func ForCluster(name string) Dir { return Dir(filepath.Join(ClustersDir(), name)) }

func (d Dir) Path(p ...string) string   { return filepath.Join(append([]string{string(d)}, p...)...) }
func (d Dir) Config() string            { return d.Path("cluster.yaml") }
func (d Dir) StateFile() string         { return d.Path("state.json") }
func (d Dir) Secret(name string) string { return d.Path("secrets", name) }
func (d Dir) Disk(name string) string   { return d.Path("disks", name) }
func (d Dir) Run(name string) string    { return d.Path("run", name) }
func (d Dir) Log(name string) string    { return d.Path("logs", name) }

func (d Dir) Exists() bool {
	_, err := os.Stat(d.StateFile())
	return err == nil
}

func (d Dir) Init() error {
	for _, sub := range []string{"", "disks", "run", "logs"} {
		if err := os.MkdirAll(d.Path(sub), 0o755); err != nil {
			return err
		}
	}
	return os.MkdirAll(d.Path("secrets"), 0o700)
}

func (d Dir) LoadConfig() (*config.Cluster, error) {
	c, err := config.Load(d.Config())
	if err != nil {
		return nil, err
	}
	c.SetDefaults()
	return c, nil
}

func (d Dir) SaveConfig(c *config.Cluster) error {
	b, err := c.YAML()
	if err != nil {
		return err
	}
	return WriteFileAtomic(d.Config(), b, 0o644)
}

func (d Dir) Load() (*State, error) {
	b, err := os.ReadFile(d.StateFile())
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cluster %q does not exist", filepath.Base(string(d)))
	}
	if err != nil {
		return nil, err
	}
	var s State
	return &s, json.Unmarshal(b, &s)
}

func (d Dir) Save(s *State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(d.StateFile(), append(b, '\n'), 0o644)
}

// Lock takes an exclusive lock on the cluster for the lifetime of the process
// (or until the returned func is called).
func (d Dir) Lock() (func(), error) {
	f, err := os.OpenFile(d.Path("lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("cluster %q is busy (another proxbase command is running)", filepath.Base(string(d)))
	}
	return func() { f.Close() }, nil
}

func (d Dir) ReadSecret(name string) (string, error) {
	b, err := os.ReadFile(d.Secret(name))
	return string(b), err
}

func (d Dir) WriteSecret(name string, data []byte) error {
	return WriteFileAtomic(d.Secret(name), data, 0o600)
}

// List returns the names of all clusters.
func List() ([]string, error) {
	entries, err := os.ReadDir(ClustersDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && ForCluster(e.Name()).Exists() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
