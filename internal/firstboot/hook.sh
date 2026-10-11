#!/bin/sh
# Proxbase first-boot hook. The Proxmox installer downloads it and the installed
# system runs it once, on its first boot, before networking and before any
# Proxmox VE service starts (proxmox-first-boot-network-pre.service).
#
# Proxbase passes data to the guest as QEMU fw_cfg files:
#   opt/proxbase/bake      script that prepares a base image; the hook runs it,
#                          stays pending for the clones and powers off
#   opt/proxbase/identity  shell variables (NODE, DOMAIN, PASSWORD, KEYS) that
#                          give a node cloned from a base image its own identity
set -eu

cfg=/sys/firmware/qemu_fw_cfg/by_name/opt/proxbase
modprobe qemu_fw_cfg 2>/dev/null || true

console() { echo "proxbase-first-boot: $*" >/dev/ttyS0 2>/dev/null || true; }

if [ -r "$cfg/bake/raw" ]; then
	status=ok
	if ! sh -eu "$cfg/bake/raw" >/var/log/proxbase-bake.log 2>&1; then
		status=failed
		tail -n 30 /var/log/proxbase-bake.log >/dev/ttyS0 2>/dev/null || true
	fi
	# Stay pending: every clone runs this hook again for its identity.
	touch /var/lib/proxmox-first-boot/pending-first-boot-setup
	console "bake $status"
	# Power off before networking and the Proxmox VE services start; the base
	# image must never run them (they create per-node keys and certificates).
	systemctl poweroff --no-block
	exec sleep infinity
fi

# pveproxy runs pveupdate (apt update, appliance list) on its first start unless
# this log exists; proxbase manages repositories and updates itself.
touch /var/log/pveam.log

grub_timeout() {
@NO_GRUB_TIMEOUT@
}
grub_timeout || console "could not remove the GRUB timeout"

if [ -r "$cfg/identity/raw" ]; then
	# shellcheck source=/dev/null
	. "$cfg/identity/raw"
	fqdn="$NODE.$DOMAIN"
	hostname "$NODE"
	echo "$NODE" >/etc/hostname
	sed -i "/^10\.0\.2\.15[[:space:]]/c\10.0.2.15 $fqdn $NODE" /etc/hosts
	# Proxmox VE names the node certificate after the search domain.
	sed -i "s/^search .*/search $DOMAIN/" /etc/resolv.conf
	echo "$fqdn" >/etc/mailname
	postconf -e "myhostname=$fqdn"
	rm -f /etc/ssh/ssh_host_*
	ssh-keygen -A >/dev/null
	rm -f /etc/machine-id /var/lib/dbus/machine-id
	systemd-machine-id-setup >/dev/null
	# journald files are per machine-id; without a restart this boot's log would
	# land under the old one, where journalctl does not look.
	systemctl restart systemd-journald
	echo "root:$PASSWORD" | chpasswd
	# Still a plain file here: pve-cluster moves it into /etc/pve on its first
	# start, so the base image's key is never trusted by the node.
	mkdir -p /root/.ssh
	printf '%s\n' "$KEYS" >/root/.ssh/authorized_keys
	chmod 600 /root/.ssh/authorized_keys
	echo "$NODE" >@IDENTITY_MARKER@
	console "identity $NODE"
fi
