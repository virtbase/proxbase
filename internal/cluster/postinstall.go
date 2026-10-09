package cluster

import (
	"fmt"
	"strings"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/qemu"
)

// postInstallScript makes a fresh node cluster-ready. It is idempotent and
// prints "changed" when it modified the network configuration.
func (c *Cluster) postInstallScript(n config.Node) string {
	var hosts, nics, ifaces strings.Builder
	for _, o := range c.Cfg.NodeList() {
		fmt.Fprintf(&hosts, "%s %s.%s %s\n", c.corosyncIP(o), o.Name, c.Cfg.Proxmox.Domain, o.Name)
	}
	for i, net := range c.Cfg.Networks {
		ip, bits, _ := config.NodeIP(net.CIDR, n.Index)
		mtu := ""
		if net.MTU != 0 {
			mtu = fmt.Sprintf("\tmtu %d\n", net.MTU)
		}
		fmt.Fprintf(&nics, "nic%d=$(ifname %s)\n", i, qemu.MAC(n.Index, i+1))
		fmt.Fprintf(&ifaces, "auto $nic%d\niface $nic%d inet manual\n%s\n", i, i, mtu)
		fmt.Fprintf(&ifaces, "auto %s\niface %s inet static\n\taddress %s/%d\n\tbridge-ports $nic%d\n\tbridge-stp off\n\tbridge-fd 0\n",
			net.Bridge, net.Bridge, ip, bits, i)
		if net.VLANAware {
			ifaces.WriteString("\tbridge-vlan-aware yes\n\tbridge-vids 2-4094\n")
		}
		ifaces.WriteString(mtu + "\n")
	}
	return fmt.Sprintf(`
# Physical NIC by MAC (bridges share the MAC, so require a device link).
ifname() {
	for d in /sys/class/net/*; do
		if [ -e "$d/device" ] && [ "$(cat "$d/address")" = "$1" ]; then basename "$d"; return; fi
	done
	echo "no interface with MAC $1" >&2; exit 1
}
block() { # file, content: replace the proxbase block
	sed -i '/^# proxbase-begin/,/^# proxbase-end/d' "$1"
	printf '# proxbase-begin\n%%s\n# proxbase-end\n' "$2" >> "$1"
}

# Hostnames resolve to the corosync network, not the shared NAT address.
sed -i '/^10\.0\.2\.15[[:space:]]/d' /etc/hosts
block /etc/hosts "$(cat <<'EOF'
%sEOF
)"

# No-subscription repository instead of enterprise.
codename=$(. /etc/os-release && echo "$VERSION_CODENAME")
for f in /etc/apt/sources.list.d/pve-enterprise.sources /etc/apt/sources.list.d/ceph.sources; do
	if [ -f "$f" ] && ! grep -q '^Enabled: false' "$f"; then echo 'Enabled: false' >> "$f"; fi
done
cat > /etc/apt/sources.list.d/proxmox.sources <<EOF
Types: deb
URIs: http://download.proxmox.com/debian/pve
Suites: $codename
Components: pve-no-subscription
Signed-By: /usr/share/keyrings/proxmox-archive-keyring.gpg
EOF

# Bridges for the internal networks.
%scp /etc/network/interfaces /etc/network/interfaces.proxbase-new
block /etc/network/interfaces.proxbase-new "$(cat <<EOF
%sEOF
)"
if cmp -s /etc/network/interfaces /etc/network/interfaces.proxbase-new; then
	rm /etc/network/interfaces.proxbase-new
else
	mv /etc/network/interfaces.proxbase-new /etc/network/interfaces
	ifreload -a
	echo changed
fi
`, hosts.String(), nics.String(), ifaces.String())
}
