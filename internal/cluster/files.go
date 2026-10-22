package cluster

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/virtbase/proxbase/internal/remote"
)

// CheckNodePath validates a file path on a node: absolute and clean.
func CheckNodePath(p string) error {
	switch {
	case !path.IsAbs(p):
		return fmt.Errorf("path %q must be absolute", p)
	case path.Clean(p) != p:
		return fmt.Errorf("path %q is not clean; use %q", p, path.Clean(p))
	case p == "/" || strings.ContainsRune(p, 0):
		return fmt.Errorf("invalid path %q", p)
	}
	return nil
}

// ParseFileMode reads octal permissions such as 0644.
func ParseFileMode(s string) (os.FileMode, error) {
	m, err := strconv.ParseUint(s, 8, 32)
	if err != nil || m > uint64(os.ModePerm) {
		return 0, fmt.Errorf("mode %q must be octal permissions like 0644", s)
	}
	return os.FileMode(m), nil
}

// PutFile writes what r yields to p on node, owned by root with mode (on
// /etc/pve, the cluster file system's own permissions). It writes a temporary
// file next to p and renames it, so readers never see a partial file; missing
// parent directories are created.
func (c *Cluster) PutFile(ctx context.Context, node, p string, r io.Reader, mode os.FileMode) error {
	if err := CheckNodePath(p); err != nil {
		return err
	}
	if mode&^os.ModePerm != 0 {
		return fmt.Errorf("mode %o has bits besides the permissions", mode)
	}
	s, err := c.nodeSSH(ctx, node)
	if err != nil {
		return err
	}
	defer s.Close()
	script := fmt.Sprintf(`set -e
p=%s
d=$(dirname -- "$p")
mkdir -p -- "$d"
t=$(mktemp -- "$d/.proxbase-XXXXXX")
trap 'rm -f -- "$t"' EXIT
cat >"$t"
# pmxcfs (/etc/pve) sets its own permissions and refuses chmod.
chmod %04[2]o -- "$t" 2>/dev/null || [ "$(stat -f -c %%T -- "$d")" = fuse ] || chmod %04[2]o -- "$t"
mv -f -- "$t" "$p"
trap - EXIT`, remote.Quote(p), mode)
	if err := s.Stream(ctx, script, r, io.Discard); err != nil {
		return fmt.Errorf("%s: write %s: %w", node, p, err)
	}
	return nil
}

// GetFile writes up to limit bytes (all with a negative limit) of p on node to
// w and returns the size of the whole file.
func (c *Cluster) GetFile(ctx context.Context, node, p string, w io.Writer, limit int64) (int64, error) {
	if err := CheckNodePath(p); err != nil {
		return 0, err
	}
	s, err := c.nodeSSH(ctx, node)
	if err != nil {
		return 0, err
	}
	defer s.Close()
	q := remote.Quote(p)
	r, err := s.Exec(ctx, "test -f "+q+" && stat -c %s -- "+q)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", node, err)
	}
	if r.ExitCode != 0 {
		return 0, fmt.Errorf("%s: %s is not a readable file", node, p)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(r.Stdout), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: size of %s: %w", node, p, err)
	}
	read := "cat -- " + q
	if limit >= 0 {
		read = fmt.Sprintf("head -c %d -- %s", limit, q)
	}
	if err := s.Stream(ctx, read, nil, w); err != nil {
		return 0, fmt.Errorf("%s: read %s: %w", node, p, err)
	}
	return size, nil
}

// nodeSSH connects to a node of the cluster.
func (c *Cluster) nodeSSH(ctx context.Context, node string) (*remote.SSH, error) {
	if c.St.Node(node) == nil {
		return nil, fmt.Errorf("cluster %s has no node %q", c.Cfg.Name, node)
	}
	s, err := c.ssh(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("%s: SSH: %w", node, err)
	}
	return s, nil
}
