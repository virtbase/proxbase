package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/cluster"
)

func cpCmd() *cobra.Command {
	var mode string
	cmd := &cobra.Command{
		Use:   "cp <source> <destination>",
		Short: "Copy a file to or from a node: [cluster:]node:/path, a local path, or - for stdin/stdout",
		Long: `cp copies one file between this host and a node, as root on the node. One side is
a node file, [cluster:]node:/path (cluster: the only cluster, else "default"); the
other a local path or - for stdin or stdout. Files on a node are replaced
atomically and missing directories created; the mode is the local file's (or
0644 from stdin) unless --mode is set.`,
		Example: `  proxbase cp playbook.yml pve1:/root/playbook.yml
  proxbase cp lab:pve2:/etc/pve/storage.cfg -
  proxbase cp - lab:pve1:/root/setup.sh --mode 0755 < setup.sh`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, srcOnNode := parseNodeFile(args[0])
			dst, dstOnNode := parseNodeFile(args[1])
			if srcOnNode == dstOnNode {
				return errors.New("one of source and destination must be a node file ([cluster:]node:/path), the other local")
			}
			if dstOnNode {
				return upload(cmd, args[0], dst, mode)
			}
			return download(cmd, src, args[1])
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "permissions of a file copied to a node, in octal (e.g. 0755)")
	return cmd
}

// nodeFile is a file on a node.
type nodeFile struct{ cluster, node, path string }

func (f nodeFile) String() string { return f.node + ":" + f.path }

// parseNodeFile reads [cluster:]node:/path; it reports false for a local path.
func parseNodeFile(s string) (nodeFile, bool) {
	i := strings.Index(s, ":/")
	if i <= 0 || strings.Contains(s[:i], "/") {
		return nodeFile{}, false
	}
	host := s[:i]
	f := nodeFile{node: host, path: s[i+1:]}
	if c, n, ok := strings.Cut(host, ":"); ok {
		f.cluster, f.node = c, n
	}
	return f, f.node != "" && !strings.Contains(f.node, ":")
}

func (f nodeFile) open() (*cluster.Cluster, error) {
	if f.cluster == "" {
		return openCluster(nil)
	}
	return openCluster([]string{f.cluster})
}

func upload(cmd *cobra.Command, local string, dst nodeFile, mode string) error {
	c, err := dst.open()
	if err != nil {
		return err
	}
	in, perm := io.Reader(os.Stdin), os.FileMode(0o644)
	if local != "-" {
		f, err := os.Open(local)
		if err != nil {
			return err
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		in, perm = f, fi.Mode().Perm()
	}
	if mode != "" {
		if perm, err = cluster.ParseFileMode(mode); err != nil {
			return err
		}
	}
	n := &countingReader{r: in}
	if err := c.PutFile(cmd.Context(), dst.node, dst.path, n, perm); err != nil {
		return err
	}
	logf("copied %d bytes to %s", n.n, dst)
	return nil
}

func download(cmd *cobra.Command, src nodeFile, local string) error {
	c, err := src.open()
	if err != nil {
		return err
	}
	if local == "-" {
		_, err := c.GetFile(cmd.Context(), src.node, src.path, os.Stdout, -1)
		return err
	}
	if fi, err := os.Stat(local); err == nil && fi.IsDir() {
		local = filepath.Join(local, filepath.Base(src.path))
	}
	// Written next to the destination and renamed, so a failed copy leaves no partial file.
	tmp, err := os.CreateTemp(filepath.Dir(local), ".proxbase-cp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	size, err := c.GetFile(cmd.Context(), src.node, src.path, tmp, -1)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), local); err != nil {
		return err
	}
	logf("copied %d bytes from %s to %s", size, src, local)
	return nil
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
