// Package network connects nodes to the cluster's internal networks without root:
// every node NIC is a unix datagram socket, and one switch process per cluster
// (the hidden `proxbase _switch <cluster>` command) forwards frames between them.
package network

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/netswitch"
	"github.com/virtbase/proxbase/internal/state"
	"github.com/virtbase/proxbase/internal/vm"
)

// Command is the hidden subcommand that runs the switch.
const Command = "_switch"

// Switch manages the switch process of one cluster.
type Switch struct {
	Dir     state.Dir
	Cluster string
}

func nodeSocket(d state.Dir, node, net string) string { return d.Run(node + "-" + net + ".sock") }
func switchSocket(d state.Dir, net string) string     { return d.Run("sw-" + net + ".sock") }

// NICs returns the NICs of node n, one per configured network, with fixed MACs.
func (s Switch) NICs(cfg *config.Cluster, n config.Node) []vm.NIC {
	nics := make([]vm.NIC, len(cfg.Networks))
	for i, net := range cfg.Networks {
		nics[i] = vm.NIC{MAC: vm.MAC(n.Index, i+1), Local: nodeSocket(s.Dir, n.Name, net.Name), Remote: switchSocket(s.Dir, net.Name)}
	}
	return nics
}

// SocketPaths returns all socket paths of node n (for length checks).
func (s Switch) SocketPaths(cfg *config.Cluster, n config.Node) []string {
	var out []string
	for _, net := range cfg.Networks {
		out = append(out, nodeSocket(s.Dir, n.Name, net.Name))
	}
	return out
}

// PID returns the switch process if it is running.
func (s Switch) PID() (int, bool) {
	b, err := os.ReadFile(s.Dir.Run("switch.pid"))
	if err != nil {
		return 0, false
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if pid <= 0 || err != nil || !strings.Contains(string(cmdline), Command+"\x00"+s.Cluster+"\x00") {
		return 0, false
	}
	return pid, true
}

// Running reports whether the switch process is alive.
func (s Switch) Running() bool {
	_, ok := s.PID()
	return ok
}

// Start launches the switch detached (new session) and waits for its sockets.
func (s Switch) Start(cfg *config.Cluster) error {
	if s.Running() {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(s.Dir.Log("switch.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, Command, s.Cluster)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	for range 50 {
		ready := true
		for _, n := range cfg.Networks {
			if _, err := os.Stat(switchSocket(s.Dir, n.Name)); err != nil {
				ready = false
			}
		}
		if ready && s.Running() {
			return nil
		}
		select {
		case <-exited:
			return fmt.Errorf("switch exited, see %s", s.Dir.Log("switch.log"))
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("switch did not start, see %s", s.Dir.Log("switch.log"))
}

// Reload makes a running switch pick up added or removed nodes.
func (s Switch) Reload(cfg *config.Cluster) error {
	pid, ok := s.PID()
	if !ok {
		return s.Start(cfg)
	}
	return syscall.Kill(pid, syscall.SIGHUP)
}

// Stop terminates the switch process.
func (s Switch) Stop() error {
	pid, ok := s.PID()
	if !ok {
		return nil
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for range 50 {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}

// Run is the body of the switch process; it serves until SIGTERM and reloads the
// node list and faults on SIGHUP.
func Run(name string) error {
	d := state.ForCluster(name)
	cfg, err := d.LoadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	peers := func(cfg *config.Cluster, net string) []string {
		var out []string
		for _, n := range cfg.NodeList() {
			out = append(out, nodeSocket(d, n.Name, net))
		}
		return out
	}
	errc := make(chan error, len(cfg.Networks))
	switches := map[string]*netswitch.Switch{}
	for _, net := range cfg.Networks {
		sw, err := netswitch.Listen(switchSocket(d, net.Name), peers(cfg, net.Name))
		if err != nil {
			return err
		}
		defer sw.Close()
		switches[net.Name] = sw
		go func() { errc <- sw.Serve(ctx) }()
	}
	apply := func(cfg *config.Cluster) {
		faults, err := loadFaults(d)
		if err != nil {
			fmt.Println("faults:", err)
		}
		for name, sw := range switches {
			sw.SetPeers(peers(cfg, name))
			sw.SetPolicy(policy(d, name, faults[name]))
		}
		if len(faults) > 0 {
			fmt.Printf("%s faults: %+v\n", time.Now().Format(time.RFC3339), faults)
		}
	}
	apply(cfg)
	// SIGHUP: nodes were added or removed, or faults changed.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for range hup {
			cfg, err := d.LoadConfig()
			if err != nil {
				fmt.Println("reload:", err)
				continue
			}
			apply(cfg)
			fmt.Printf("%s reloaded: %d nodes\n", time.Now().Format(time.RFC3339), cfg.Nodes.Count)
		}
	}()
	if err := state.WriteFileAtomic(d.Run("switch.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		return err
	}
	defer os.Remove(d.Run("switch.pid"))
	fmt.Printf("%s switch for %s up (%d networks)\n", time.Now().Format(time.RFC3339), name, len(cfg.Networks))
	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		return err
	}
}
