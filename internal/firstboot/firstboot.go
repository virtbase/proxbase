// Package firstboot prepares nodes on their first boot through the Proxmox
// installer's first-boot hook. The installer downloads the hook from a small
// HTTP server on the host and the installed system runs it once, before
// networking and before any Proxmox VE service starts. Proxbase uses that early
// moment to:
//
//   - give nodes cloned from a base image their own identity without a reboot,
//   - bake a base image (prepare it once, then power off unbooted by Proxmox VE),
//   - skip work Proxmox VE does on a first boot that proxbase does not need.
//
// Per-node data reaches the hook as QEMU fw_cfg files (vm.File).
package firstboot

import (
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/virtbase/proxbase/internal/nodesetup"
)

const (
	// FileBake is the guest file name of the bake script.
	FileBake = "opt/proxbase/bake"
	// FileIdentity is the guest file name of a node's identity.
	FileIdentity = "opt/proxbase/identity"
	// IdentityMarker exists on nodes that received their identity; it holds the node name.
	IdentityMarker = "/etc/proxbase-identity"
	// BakeOK is printed on the serial console when a bake succeeded.
	BakeOK = "proxbase-first-boot: bake ok"

	// hostAddr is the host as seen from a guest on QEMU user networking.
	hostAddr = "10.0.2.2"
	hookPath = "/proxmox-first-boot"
)

//go:embed hook.sh
var hookTemplate string

// Hook returns the first-boot hook script.
func Hook() string {
	return strings.NewReplacer(
		"@NO_GRUB_TIMEOUT@", strings.Trim(nodesetup.NoGrubTimeout, "\n"),
		"@IDENTITY_MARKER@", IdentityMarker,
	).Replace(hookTemplate)
}

// Server serves the hook to installers on QEMU user networking.
type Server struct {
	srv *http.Server
	// URL is the hook's address as seen from the guest.
	URL string
}

// Serve starts serving the hook on a free port of the host's loopback address,
// which guests on QEMU user networking reach as 10.0.2.2.
func Serve() (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("first-boot hook server: %w", err)
	}
	hook := []byte(Hook())
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+hookPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/x-shellscript")
		_, _ = w.Write(hook)
	})
	s := &Server{
		srv: &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second},
		URL: "http://" + net.JoinHostPort(hostAddr, strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)) + hookPath,
	}
	// Serve returns when Close is called. Any other failure surfaces as a failed
	// download in the installer, which reports it.
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// Close stops the server.
func (s *Server) Close() error { return s.srv.Close() }

// Identity renders the identity file of a node cloned from a base image.
func Identity(node, domain, password string, authorizedKeys []string) []byte {
	keys := make([]string, len(authorizedKeys))
	for i, k := range authorizedKeys {
		keys[i] = strings.TrimSpace(k)
	}
	return fmt.Appendf(nil, "NODE=%s\nDOMAIN=%s\nPASSWORD=%s\nKEYS=%s\n",
		quote(node), quote(domain), quote(password), quote(strings.Join(keys, "\n")))
}

// Bake returns the script that prepares a base image on its only boot. With a
// Ceph release (e.g. "squid") it also installs the Ceph packages, so nodes
// cloned from the image skip `pveceph install`; natMAC is the MAC of the NIC on
// QEMU user networking it downloads through.
func Bake(cephRelease, natMAC string) string {
	var b strings.Builder
	b.WriteString("touch /var/log/pveam.log\n")
	b.WriteString(nodesetup.NoGrubTimeout)
	if cephRelease != "" {
		b.WriteString(nodesetup.Repositories)
		fmt.Fprintf(&b, cephBake, cephRelease, natMAC)
	}
	return b.String()
}

// cephBake installs what `pveceph install` installs. Networking is not up yet,
// so it configures the NAT NIC itself with ip: ifupdown2 cannot be used here,
// its lock is held by hotplug ifup runs that wait for networking, which waits
// for this hook. policy-rc.d keeps packages from starting services during the
// boot.
const cephBake = `
cat > /etc/apt/sources.list.d/ceph.sources <<EOF
Types: deb
URIs: http://download.proxmox.com/debian/ceph-%[1]s
Suites: $codename
Components: no-subscription
Signed-By: /usr/share/keyrings/proxmox-archive-keyring.gpg
EOF
nic=
for d in /sys/class/net/*; do
	if [ -e "$d/device" ] && [ "$(cat "$d/address")" = %[2]q ]; then nic=$(basename "$d"); fi
done
[ -n "$nic" ] || { echo "no NIC with MAC %[2]s" >&2; exit 1; }
ip link set "$nic" up
ip addr add 10.0.2.15/24 dev "$nic"
ip route add default via 10.0.2.2
printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d
chmod 755 /usr/sbin/policy-rc.d
trap 'rm -f /usr/sbin/policy-rc.d' EXIT
export DEBIAN_FRONTEND=noninteractive
apt='apt-get -o Acquire::Retries=3 -o Dpkg::Options::=--force-unsafe-io'
retry_apt() { "$@" || "$@" -o Acquire::http::No-Cache=true; }
$apt update
retry_apt $apt install -y --no-install-recommends -o Dpkg::Options::=--force-confnew \
	ceph ceph-common ceph-fuse ceph-mds ceph-volume gdisk nvme-cli
retry_apt $apt install -y --only-upgrade libpve-storage-perl
$apt clean
ip addr flush dev "$nic"
ip link set "$nic" down
`

// quote returns s single-quoted for the shell.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
