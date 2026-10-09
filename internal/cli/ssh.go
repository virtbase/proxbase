package cli

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/state"
)

func sshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ssh [cluster] [node] [-- command...]",
		Short: "SSH to a node as root (default: the first node)",
		RunE: func(cmd *cobra.Command, args []string) error {
			pos, rest := args, []string(nil)
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				pos, rest = args[:dash], args[dash:]
			}
			if len(pos) > 2 {
				return errors.New("usage: proxbase ssh [cluster] [node] [-- command...]")
			}
			c, node, err := clusterNode(pos)
			if err != nil {
				return err
			}
			known := c.Dir.Run("known_hosts")
			if err := writeKnownHosts(c, known); err != nil {
				return err
			}
			bin, err := exec.LookPath("ssh")
			if err != nil {
				return err
			}
			argv := []string{"ssh", "-i", c.KeyPath(), "-o", "IdentitiesOnly=yes", "-o", "UserKnownHostsFile=" + known,
				"-o", "StrictHostKeyChecking=yes", "-p", strconv.Itoa(node.SSHPort), "root@127.0.0.1"}
			return syscall.Exec(bin, append(argv, rest...), os.Environ())
		},
	}
}

// clusterNode resolves [cluster] [node]: one argument is a node of the default
// cluster if such a node exists, else a cluster name.
func clusterNode(args []string) (*cluster.Cluster, *state.NodeState, error) {
	var cargs []string
	nodeName := ""
	switch len(args) {
	case 2:
		cargs, nodeName = args[:1], args[1]
	case 1:
		if c, err := openCluster(nil); err == nil && c.St.Node(args[0]) != nil {
			return c, c.St.Node(args[0]), nil
		}
		cargs = args
	}
	c, err := openCluster(cargs)
	if err != nil {
		return nil, nil, err
	}
	if nodeName == "" {
		return c, c.St.Nodes[0], nil
	}
	n := c.St.Node(nodeName)
	if n == nil {
		return nil, nil, fmt.Errorf("cluster %s has no node %q", c.Cfg.Name, nodeName)
	}
	return c, n, nil
}

func writeKnownHosts(c *cluster.Cluster, path string) error {
	var b strings.Builder
	for _, n := range c.St.Nodes {
		raw, err := base64.StdEncoding.DecodeString(n.HostKey)
		if err != nil || n.HostKey == "" {
			continue
		}
		key, err := ssh.ParsePublicKey(raw)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "[127.0.0.1]:%d %s %s\n", n.SSHPort, key.Type(), n.HostKey)
	}
	return state.WriteFileAtomic(path, []byte(b.String()), 0o600)
}

func consoleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "console [cluster] <node>",
		Short: "Attach to a node's serial console (Ctrl-] to detach)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			c, node, err := clusterNode(args)
			if err != nil {
				return err
			}
			sock, err := c.ConsoleSocket(node.Name)
			if err != nil {
				return err
			}
			conn, err := net.Dial("unix", sock)
			if err != nil {
				return fmt.Errorf("%s: console not available (is the node running?): %w", node.Name, err)
			}
			defer conn.Close()
			fd := int(os.Stdin.Fd())
			if term.IsTerminal(fd) {
				old, err := term.MakeRaw(fd)
				if err != nil {
					return err
				}
				defer func() { _ = term.Restore(fd, old) }()
			}
			fmt.Fprintf(os.Stderr, "Connected to %s (Ctrl-] to detach). Press Enter for a login prompt.\r\n", node.Name)
			go func() { _, _ = io.Copy(os.Stdout, conn) }()
			buf := make([]byte, 256)
			for {
				n, err := os.Stdin.Read(buf)
				if err != nil {
					return nil
				}
				if i := strings.IndexByte(string(buf[:n]), 0x1d); i >= 0 {
					_, _ = conn.Write(buf[:i])
					fmt.Fprint(os.Stderr, "\r\nDetached.\r\n")
					return nil
				}
				if _, err := conn.Write(buf[:n]); err != nil {
					return err
				}
			}
		},
	}
}
