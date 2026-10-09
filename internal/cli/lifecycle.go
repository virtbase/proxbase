package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/state"
)

func printStatus(s *cluster.Status, output string) error {
	if output == "json" {
		return printJSON(s)
	}
	quorate := "-"
	for _, n := range s.Nodes {
		if n.Running {
			quorate = "unknown (API not reachable)"
		}
	}
	if s.Quorate != nil {
		quorate = map[bool]string{true: "yes", false: "NO"}[*s.Quorate]
	}
	fmt.Printf("Cluster %s: %s, quorate: %s, switch: %s\n", s.Name, s.Phase, quorate, map[bool]string{true: "running", false: "stopped"}[s.Switch])
	if s.Ceph != "" {
		fmt.Printf("Ceph: %s\n", s.Ceph)
	}
	for _, f := range s.Faults {
		fmt.Printf("Fault: %s\n", f)
	}
	if s.Error != "" {
		fmt.Printf("Last error: %s\n", firstLine(s.Error))
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NODE\tVM\tCLUSTER\tIP\tWEB UI\tSSH")
	for _, n := range s.Nodes {
		online := "-"
		if n.Online != nil {
			online = map[bool]string{true: "online", false: "offline"}[*n.Online]
		}
		vm := map[bool]string{true: "running", false: "stopped"}[n.Running]
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t127.0.0.1:%d\n", n.Name, vm, online, n.IP, n.UI, n.SSHPort)
	}
	return w.Flush()
}

func firstLine(s string) string { return strings.SplitN(s, "\n", 2)[0] }

func listCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List clusters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			names, err := state.List()
			if err != nil {
				return err
			}
			type row struct {
				Name    string `json:"name"`
				Phase   string `json:"phase"`
				Nodes   int    `json:"nodes"`
				Running int    `json:"running"`
			}
			rows := []row{}
			for _, name := range names {
				c, err := cluster.Open(name, logf)
				if err != nil {
					rows = append(rows, row{Name: name, Phase: "broken: " + err.Error()})
					continue
				}
				s := c.Status(cmd.Context())
				r := row{Name: name, Phase: s.Phase, Nodes: len(s.Nodes)}
				for _, n := range s.Nodes {
					if n.Running {
						r.Running++
					}
				}
				rows = append(rows, r)
			}
			if output == "json" {
				return printJSON(rows)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tPHASE\tNODES\tRUNNING")
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%d\t%d\n", r.Name, r.Phase, r.Nodes, r.Running)
			}
			return w.Flush()
		},
	}
	outputFlag(cmd, &output)
	return cmd
}

// healthy explains why a cluster is not fully healthy ("" if it is).
func healthy(s *cluster.Status) string {
	switch {
	case s.Phase != "ready":
		return "phase " + s.Phase
	case s.Quorate == nil || !*s.Quorate:
		return "not quorate"
	}
	for _, n := range s.Nodes {
		if !n.Running || n.Online == nil || !*n.Online {
			return n.Name + " not running or offline"
		}
	}
	if s.Ceph != "" && s.Ceph != "HEALTH_OK" {
		return "ceph " + s.Ceph
	}
	return ""
}

func statusCmd() *cobra.Command {
	var output string
	var check bool
	cmd := &cobra.Command{
		Use:   "status [name]",
		Short: "Show nodes, quorum and ports",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openCluster(args)
			if err != nil {
				return err
			}
			s := c.Status(cmd.Context())
			if check {
				if why := healthy(s); why != "" {
					return fmt.Errorf("cluster %s is not healthy: %s", s.Name, why)
				}
				fmt.Printf("cluster %s is healthy\n", s.Name)
				return nil
			}
			return printStatus(s, output)
		},
	}
	outputFlag(cmd, &output)
	cmd.Flags().BoolVar(&check, "check", false, "exit non-zero unless ready, quorate, all nodes online and Ceph HEALTH_OK (for health checks)")
	return cmd
}

func startCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start [name]",
		Short: "Start a stopped cluster and wait for quorum",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openCluster(args)
			if err != nil {
				return err
			}
			if err := c.Start(cmd.Context()); err != nil {
				return err
			}
			return printStatus(c.Status(cmd.Context()), "table")
		},
	}
}

func stopCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "stop [name]",
		Short: "Shut down all nodes",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openCluster(args)
			if err != nil {
				return err
			}
			return c.Stop(cmd.Context(), timeout)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "time to wait for a clean shutdown (nodes shut down their guests first) before powering off hard; 0 = hard")
	return cmd
}

func destroyCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "destroy [name]",
		Short: "Delete a cluster: kill its VMs and remove all its files",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := clusterName(args)
			if !yes {
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return fmt.Errorf("refusing to destroy %q without --yes", name)
				}
				fmt.Fprintf(os.Stderr, "Destroy cluster %q and all its disks? Type the name to confirm: ", name)
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if strings.TrimSpace(line) != name {
					return fmt.Errorf("aborted")
				}
			}
			if err := cluster.Destroy(name, logf); err != nil {
				return err
			}
			logf("cluster %s destroyed", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	return cmd
}
