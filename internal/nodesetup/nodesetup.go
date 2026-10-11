// Package nodesetup generates the shell scripts that prepare installed nodes:
// host names, repositories, bridges for the internal networks and upgrades.
package nodesetup

import (
	"fmt"
	"strings"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/vm"
)

// Script makes node n cluster-ready. It is idempotent and prints "changed" when
// it modified the network configuration.
func Script(cfg *config.Cluster, n config.Node) string {
	var hosts, nics, ifaces strings.Builder
	link0 := cfg.CorosyncNetwork()
	for _, o := range cfg.NodeList() {
		ip, _, _ := config.NodeIP(link0.CIDR, o.Index)
		fmt.Fprintf(&hosts, "%s %s.%s %s\n", ip, o.Name, cfg.Proxmox.Domain, o.Name)
	}
	for i, net := range cfg.Networks {
		mtu := ""
		if net.MTU != 0 {
			mtu = fmt.Sprintf("\tmtu %d\n", net.MTU)
		}
		fmt.Fprintf(&nics, "nic%d=$(ifname %s)\n", i, vm.MAC(n.Index, i+1))
		fmt.Fprintf(&ifaces, "auto $nic%d\niface $nic%d inet manual\n%s\n", i, i, mtu)
		if net.CIDR == "" {
			fmt.Fprintf(&ifaces, "auto %s\niface %s inet manual\n", net.Bridge, net.Bridge)
		} else {
			ip, bits, _ := config.NodeIP(net.CIDR, n.Index)
			fmt.Fprintf(&ifaces, "auto %s\niface %s inet static\n\taddress %s/%d\n", net.Bridge, net.Bridge, ip, bits)
		}
		fmt.Fprintf(&ifaces, "\tbridge-ports $nic%d\n\tbridge-stp off\n\tbridge-fd 0\n", i)
		if net.VLANAware {
			vids := "2-4094"
			if len(net.VLANs) > 0 {
				vids = strings.Join(net.VLANs, " ")
			}
			fmt.Fprintf(&ifaces, "\tbridge-vlan-aware yes\n\tbridge-vids %s\n", vids)
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

# Login prompt on the serial console (proxbase console).
systemctl enable --now serial-getty@ttyS0.service >/dev/null 2>&1
%s
# No-subscription repository instead of enterprise.
%s
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
`, hosts.String(), NoGrubTimeout, Repositories, nics.String(), ifaces.String())
}

// Repositories replaces the enterprise repositories (they need a subscription)
// with the no-subscription repository. A ceph.sources that already points
// elsewhere (pveceph install, a baked base image) is left alone.
const Repositories = `
codename=$(. /etc/os-release && echo "$VERSION_CODENAME")
for f in /etc/apt/sources.list.d/pve-enterprise.sources /etc/apt/sources.list.d/ceph.sources; do
	if [ -f "$f" ] && grep -q 'enterprise.proxmox.com' "$f" && ! grep -q '^Enabled: false' "$f"; then
		echo 'Enabled: false' >> "$f"
	fi
done
cat > /etc/apt/sources.list.d/proxmox.sources <<EOF
Types: deb
URIs: http://download.proxmox.com/debian/pve
Suites: $codename
Components: pve-no-subscription
Signed-By: /usr/share/keyrings/proxmox-archive-keyring.gpg
EOF
`

// NoGrubTimeout makes the node boot without waiting in the GRUB menu (Debian
// waits 5 seconds), which shortens every reboot and start.
const NoGrubTimeout = `
if grep -q '^GRUB_TIMEOUT=[1-9]' /etc/default/grub; then
	sed -i 's/^GRUB_TIMEOUT=.*/GRUB_TIMEOUT=0/' /etc/default/grub
	if proxmox-boot-tool status >/dev/null 2>&1; then
		proxmox-boot-tool refresh >/dev/null 2>&1
	else
		update-grub >/dev/null 2>&1
	fi
fi
`

// ProvisionStart speeds up writes while proxbase provisions a node: ZFS treats
// fsync as a no-op and dpkg skips its per-file syncs. ProvisionEnd restores
// both, so a ready cluster has real fsync semantics for fault injection. It also
// turns on autotrim so freed space shrinks the disk images.
const ProvisionStart = `
if zpool list rpool >/dev/null 2>&1; then
	zfs set sync=disabled rpool
	zpool set autotrim=on rpool
fi
echo force-unsafe-io > /etc/dpkg/dpkg.cfg.d/proxbase-unsafe-io
`

// ProvisionEnd undoes ProvisionStart's write shortcuts.
const ProvisionEnd = `
if zpool list rpool >/dev/null 2>&1; then zfs set sync=standard rpool; fi
rm -f /etc/dpkg/dpkg.cfg.d/proxbase-unsafe-io
`

// AptWait waits for apt runs started by PVE itself (pveupdate right after boot)
// and sets $apt to an apt-get that waits for locks and retries downloads.
// retry_apt runs an apt command again past any cache if it fails: a broken
// download (Hash Sum mismatch) is then fetched anew, also by the host's cache.
const AptWait = `
for _ in $(seq 300); do pgrep -x 'apt-get|apt|dpkg|unattended-upgr' >/dev/null || break; sleep 2; done
apt='apt-get -o DPkg::Lock::Timeout=600 -o Acquire::Retries=3'
retry_apt() { "$@" || "$@" -o Acquire::http::No-Cache=true; }
`

// Upgrade brings a node to the newest no-subscription packages and prints the
// apt summary line.
const Upgrade = AptWait + `
export DEBIAN_FRONTEND=noninteractive
log=/var/log/proxbase-upgrade.log
if ! { $apt update && retry_apt $apt -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold dist-upgrade; } >$log 2>&1; then
	tail -n 20 $log >&2; exit 1
fi
grep -E '^[0-9]+ upgraded' $log || true
`

// PendingKernel prints "<kernel> <boot id>" if the newest installed kernel is
// not the running one, and nothing otherwise.
const PendingKernel = `new=$(ls /boot/vmlinuz-* | sed 's#^/boot/vmlinuz-##' | sort -V | tail -n 1)
if [ "$new" != "$(uname -r)" ]; then echo "$new $(cat /proc/sys/kernel/random/boot_id)"; fi`

// Reboot reboots two seconds later, so the SSH session can end cleanly.
const Reboot = "systemd-run --on-active=2 systemctl reboot >/dev/null"

// RepairJoin repairs what a cluster join can leave broken. The joining node
// finishes its setup before the cluster file system has synced to it: files it
// writes then (its key and certificate) can be lost, and if it believes the
// cluster CA is missing it writes a new one, which leaves the other nodes with
// certificates of the old CA. A failed join also skips restarting pvedaemon and
// pveproxy, which then reject new tickets. RepairJoin issues a new node
// certificate from the current cluster CA and restarts both services if the
// certificate is missing or not signed by that CA, or the node's last join
// failed. It prints "repaired" when it acted.
const RepairJoin = `
marker=/var/lib/proxbase-join-repaired
join=$(grep ':clusterjoin:' /var/log/pve/tasks/index 2>/dev/null | tail -n 1 || true)
failed=
if [ -n "$join" ] && ! echo "$join" | grep -q ' OK$' && [ "$(cat $marker 2>/dev/null)" != "$join" ]; then
	failed=1
fi
if [ -n "$failed" ] || [ ! -s /etc/pve/local/pve-ssl.key ] ||
	! openssl verify -CAfile /etc/pve/pve-root-ca.pem /etc/pve/local/pve-ssl.pem >/dev/null 2>&1; then
	pvecm updatecerts --force --silent
	systemctl restart pvedaemon pveproxy
	if [ -n "$join" ]; then echo "$join" > $marker; fi
	echo repaired
fi
`

// BootID prints the boot ID and the running kernel.
const BootID = "cat /proc/sys/kernel/random/boot_id; uname -r"
