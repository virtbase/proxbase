package network

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/netswitch"
	"github.com/virtbase/proxbase/internal/state"
)

// Faults are injected network faults per network name, kept in run/faults.json.
type Faults map[string]Fault

// Fault degrades one network.
type Fault struct {
	// Partition lists groups of node names that can only reach their own group;
	// nodes not listed form one more group.
	Partition [][]string    `json:"partition,omitempty"`
	Delay     time.Duration `json:"delay,omitempty"`
	Loss      float64       `json:"loss,omitempty"`
}

func faultsFile(d state.Dir) string { return d.Run("faults.json") }

// Faults returns the active network faults.
func (s Switch) Faults() (Faults, error) { return loadFaults(s.Dir) }

func loadFaults(d state.Dir) (Faults, error) {
	b, err := os.ReadFile(faultsFile(d))
	if errors.Is(err, os.ErrNotExist) {
		return Faults{}, nil
	}
	if err != nil {
		return nil, err
	}
	f := Faults{}
	return f, json.Unmarshal(b, &f)
}

// DropFaults forgets all faults without touching the switch (it is stopped).
func (s Switch) DropFaults() error {
	if err := os.Remove(faultsFile(s.Dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// SetFaults stores the faults and makes the running switch apply them.
func (s Switch) SetFaults(cfg *config.Cluster, f Faults) error {
	if len(f) == 0 {
		if err := os.Remove(faultsFile(s.Dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		b, err := json.MarshalIndent(f, "", "  ")
		if err != nil {
			return err
		}
		if err := state.WriteFileAtomic(faultsFile(s.Dir), b, 0o644); err != nil {
			return err
		}
	}
	return s.Reload(cfg)
}

// policy turns a fault into a switch policy for network net.
func policy(d state.Dir, net string, f Fault) netswitch.Policy {
	p := netswitch.Policy{Delay: f.Delay, Loss: f.Loss}
	if len(f.Partition) == 0 {
		return p
	}
	group := map[string]int{} // socket path -> group; unlisted nodes are group -1
	for i, g := range f.Partition {
		for _, node := range g {
			group[nodeSocket(d, node, net)] = i + 1
		}
	}
	p.Block = func(from, to string) bool { return group[from] != group[to] }
	return p
}
