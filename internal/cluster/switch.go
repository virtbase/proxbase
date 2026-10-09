package cluster

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

	"github.com/virtbase/proxbase/internal/netswitch"
	"github.com/virtbase/proxbase/internal/state"
)

// SwitchCommand is the hidden subcommand that runs the per-cluster switch.
const SwitchCommand = "_switch"

func (c *Cluster) switchPID() (int, bool) {
	b, err := os.ReadFile(c.Dir.Run("switch.pid"))
	if err != nil {
		return 0, false
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if pid <= 0 || err != nil || !strings.Contains(string(cmdline), SwitchCommand+"\x00"+c.Cfg.Name+"\x00") {
		return 0, false
	}
	return pid, true
}

// startSwitch launches `proxbase _switch <name>` detached and waits for its sockets.
func (c *Cluster) startSwitch() error {
	if _, ok := c.switchPID(); ok {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(c.Dir.Log("switch.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, SwitchCommand, c.Cfg.Name)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	for i := 0; i < 50; i++ {
		ready := true
		for _, n := range c.Cfg.Networks {
			if _, err := os.Stat(c.switchSock(n.Name)); err != nil {
				ready = false
			}
		}
		if _, ok := c.switchPID(); ok && ready {
			return nil
		}
		select {
		case <-exited:
			return fmt.Errorf("switch exited, see %s", c.Dir.Log("switch.log"))
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("switch did not start, see %s", c.Dir.Log("switch.log"))
}

func (c *Cluster) stopSwitch() error {
	pid, ok := c.switchPID()
	if !ok {
		return nil
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for i := 0; i < 50; i++ {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}

// RunSwitch is the body of the hidden switch command; it runs until SIGTERM.
func RunSwitch(name string) error {
	d := state.ForCluster(name)
	cfg, err := d.LoadConfig()
	if err != nil {
		return err
	}
	c := &Cluster{Dir: d, Cfg: cfg}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	errc := make(chan error, len(cfg.Networks))
	for _, net := range cfg.Networks {
		var peers []string
		for _, n := range cfg.NodeList() {
			peers = append(peers, c.nodeSock(n, net.Name))
		}
		sw, err := netswitch.Listen(c.switchSock(net.Name), peers)
		if err != nil {
			return err
		}
		defer sw.Close()
		go func() { errc <- sw.Serve(ctx) }()
	}
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
