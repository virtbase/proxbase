package ceph

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/virtbase/proxbase/internal/nodesetup"
	"github.com/virtbase/proxbase/internal/storage"
)

// installScript installs Ceph from the no-subscription repository. Ceph keys since
// 19.2.6 (aes256k) are rejected by the libpve-storage-perl on the 9.2 ISO ("Not a
// proper rbd authentication file"), so that package is upgraded too.
func installScript(version string) string {
	return nodesetup.AptWait + fmt.Sprintf(`
export DEBIAN_FRONTEND=noninteractive
log=/var/log/proxbase-ceph-install.log
if ! [ -x /usr/bin/ceph-mon ] || ! ceph-mon --version 2>/dev/null | grep -q ' %[1]s '; then
	if ! { yes || true; } | pveceph install --repository no-subscription --version %[1]s >$log 2>&1; then
		tail -n 20 $log >&2; exit 1
	fi
	echo installed
fi
before=$(dpkg-query -W -f '${Version}' libpve-storage-perl)
if ! $apt install -y --only-upgrade libpve-storage-perl >>$log 2>&1; then
	tail -n 20 $log >&2; exit 1
fi
if [ "$before" != "$(dpkg-query -W -f '${Version}' libpve-storage-perl)" ]; then
	systemctl restart pvedaemon pveproxy pvestatd
	echo "upgraded libpve-storage-perl $before -> $(dpkg-query -W -f '${Version}' libpve-storage-perl)"
fi
`, version)
}

// install runs the install script on all nodes in parallel.
func (b *Backend) install(ctx context.Context, h storage.Host) error {
	script := installScript(b.cfg.Version)
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range h.Nodes() {
		g.Go(func() error {
			s, err := h.SSH(gctx, n.Name)
			if err != nil {
				return err
			}
			defer s.Close()
			t0 := time.Now()
			out, err := s.Run(script)
			if err != nil {
				return fmt.Errorf("%s: pveceph install: %w", n.Name, err)
			}
			if strings.Contains(out, "installed") {
				h.Step("%s: ceph %s installed in %s", n.Name, b.cfg.Version, time.Since(t0).Round(time.Second))
			}
			if i := strings.Index(out, "upgraded "); i >= 0 {
				h.Step("%s: %s", n.Name, strings.TrimSpace(out[i:]))
			}
			return nil
		})
	}
	return g.Wait()
}
