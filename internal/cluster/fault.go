package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/network"
	"github.com/virtbase/proxbase/internal/state"
	"github.com/virtbase/proxbase/internal/vm"
)

// Fault kinds of node faults.
const (
	FaultKill     = "kill"
	FaultFreeze   = "freeze"
	FaultLinkDown = "link-down"
)

func (c *Cluster) injector() (vm.FaultInjector, error) {
	fi, ok := c.runtime.(vm.FaultInjector)
	if !ok {
		return nil, errors.New("the node runtime does not support fault injection")
	}
	return fi, nil
}

// runningNode returns a node that must exist and run.
func (c *Cluster) runningNode(name string) (config.Node, error) {
	for _, n := range c.Cfg.NodeList() {
		if n.Name == name {
			if !c.running(n) {
				return n, fmt.Errorf("%s is not running", name)
			}
			return n, nil
		}
	}
	return config.Node{}, fmt.Errorf("cluster %s has no node %q", c.Cfg.Name, name)
}

// network returns the index and config of a network; "" means corosync link0.
func (c *Cluster) network(name string) (int, config.Network, error) {
	if name == "" {
		name = c.Cfg.CorosyncNetwork().Name
	}
	for i, n := range c.Cfg.Networks {
		if n.Name == name {
			return i, n, nil
		}
	}
	return 0, config.Network{}, fmt.Errorf("cluster %s has no network %q", c.Cfg.Name, name)
}

// nodeFault runs f on a running node and records or clears the fault.
func (c *Cluster) nodeFault(name string, record *state.Fault, clear func(state.Fault) bool, f func(vm.FaultInjector, vm.Spec) error) error {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	fi, err := c.injector()
	if err != nil {
		return err
	}
	n, err := c.runningNode(name)
	if err != nil {
		return err
	}
	if err := f(fi, c.spec(n)); err != nil {
		return err
	}
	if clear != nil {
		c.St.Faults = slices.DeleteFunc(c.St.Faults, clear)
	}
	if record != nil && !slices.Contains(c.St.Faults, *record) {
		c.St.Faults = append(c.St.Faults, *record)
	}
	return c.save()
}

// KillNode powers a node off hard; `start` boots it again.
func (c *Cluster) KillNode(name string) error {
	return c.nodeFault(name, &state.Fault{Kind: FaultKill, Node: name}, nil,
		func(fi vm.FaultInjector, s vm.Spec) error { return fi.Kill(s) })
}

// FreezeNode stops a node's vCPUs.
func (c *Cluster) FreezeNode(ctx context.Context, name string) error {
	return c.nodeFault(name, &state.Fault{Kind: FaultFreeze, Node: name}, nil,
		func(fi vm.FaultInjector, s vm.Spec) error { return fi.Freeze(ctx, s) })
}

// ThawNode resumes a frozen node.
func (c *Cluster) ThawNode(ctx context.Context, name string) error {
	return c.nodeFault(name, nil, func(f state.Fault) bool { return f.Kind == FaultFreeze && f.Node == name },
		func(fi vm.FaultInjector, s vm.Spec) error { return fi.Thaw(ctx, s) })
}

// SetLink pulls (up=false) or plugs in the cable of a node's NIC on a network.
func (c *Cluster) SetLink(ctx context.Context, name, net string, up bool) error {
	i, nw, err := c.network(net)
	if err != nil {
		return err
	}
	f := state.Fault{Kind: FaultLinkDown, Node: name, Network: nw.Name}
	var record *state.Fault
	if !up {
		record = &f
	}
	return c.nodeFault(name, record, func(x state.Fault) bool { return up && x == f },
		func(fi vm.FaultInjector, s vm.Spec) error { return fi.SetLink(ctx, s, i, up) })
}

// Partition splits a network into groups of nodes that only reach their own
// group; nodes not listed form one more group.
func (c *Cluster) Partition(net string, groups [][]string) error {
	return c.netFault(net, func(f *network.Fault) error {
		names := c.nodeNames()
		for _, g := range groups {
			for _, n := range g {
				if !slices.Contains(names, n) {
					return fmt.Errorf("cluster %s has no node %q", c.Cfg.Name, n)
				}
			}
		}
		f.Partition = groups
		return nil
	})
}

// Degrade adds latency and frame loss to a network.
func (c *Cluster) Degrade(net string, delay time.Duration, loss float64) error {
	if delay < 0 || loss < 0 || loss > 1 {
		return errors.New("delay must be >= 0 and loss between 0 and 1")
	}
	return c.netFault(net, func(f *network.Fault) error {
		f.Delay, f.Loss = delay, loss
		return nil
	})
}

func (c *Cluster) netFault(net string, change func(*network.Fault) error) error {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	_, nw, err := c.network(net)
	if err != nil {
		return err
	}
	if !c.net.Running() {
		return errors.New("the cluster's switch is not running; start the cluster first")
	}
	faults, err := c.net.Faults()
	if err != nil {
		return err
	}
	f := faults[nw.Name]
	if err := change(&f); err != nil {
		return err
	}
	faults[nw.Name] = f
	return c.net.SetFaults(c.Cfg, faults)
}

// ClearFaults thaws frozen nodes, reconnects links and removes network faults.
// Killed nodes stay off; `start` boots them.
func (c *Cluster) ClearFaults(ctx context.Context) ([]string, error) {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	var done []string
	fi, ferr := c.injector()
	for _, f := range c.St.Faults {
		n, err := c.runningNode(f.Node)
		if err != nil || ferr != nil {
			continue
		}
		switch f.Kind {
		case FaultFreeze:
			err = fi.Thaw(ctx, c.spec(n))
		case FaultLinkDown:
			if i, _, nerr := c.network(f.Network); nerr == nil {
				err = fi.SetLink(ctx, c.spec(n), i, true)
			}
		}
		if err != nil {
			return done, fmt.Errorf("clear %s on %s: %w", f.Kind, f.Node, err)
		}
		done = append(done, describe(f))
	}
	c.St.Faults = nil
	if err := c.save(); err != nil {
		return done, err
	}
	faults, err := c.net.Faults()
	if err != nil {
		return done, err
	}
	for name := range faults {
		done = append(done, "network "+name)
	}
	if len(faults) > 0 {
		return done, c.net.SetFaults(c.Cfg, network.Faults{})
	}
	return done, nil
}

func describe(f state.Fault) string {
	if f.Network != "" {
		return fmt.Sprintf("%s %s (%s)", f.Kind, f.Node, f.Network)
	}
	return f.Kind + " " + f.Node
}

// Faults describes all active faults.
func (c *Cluster) Faults() []string {
	var out []string
	for _, f := range c.St.Faults {
		out = append(out, describe(f))
	}
	faults, _ := c.net.Faults()
	for name, f := range faults {
		var parts []string
		if len(f.Partition) > 0 {
			var groups []string
			for _, g := range f.Partition {
				groups = append(groups, strings.Join(g, ","))
			}
			parts = append(parts, "partition "+strings.Join(groups, " | ")+" | rest")
		}
		if f.Delay > 0 {
			parts = append(parts, "delay "+f.Delay.String())
		}
		if f.Loss > 0 {
			parts = append(parts, fmt.Sprintf("loss %g%%", f.Loss*100))
		}
		if len(parts) > 0 {
			out = append(out, "network "+name+": "+strings.Join(parts, ", "))
		}
	}
	slices.Sort(out)
	return out
}
