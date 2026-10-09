package golden

import (
	"fmt"
	"strings"
)

// IdentityMarker exists on nodes that were personalized.
const IdentityMarker = "/etc/proxbase-identity"

// Personalize returns the script that gives a node cloned from a base image its
// own identity. The node must reboot afterwards (Proxmox services read the host
// name at start); Finish completes it after the reboot.
func Personalize(node, domain, password string, authorizedKeys []string) string {
	fqdn := node + "." + domain
	return fmt.Sprintf(`
node=%[1]s fqdn=%[2]s
hostname "$node"
echo "$node" > /etc/hostname
sed -i "/^10\.0\.2\.15[[:space:]]/c\10.0.2.15 $fqdn $node" /etc/hosts
echo "$fqdn" > /etc/mailname
postconf -e "myhostname=$fqdn"
rm -f /etc/ssh/ssh_host_*
ssh-keygen -A >/dev/null
rm -f /etc/machine-id /var/lib/dbus/machine-id
systemd-machine-id-setup >/dev/null
echo "root:%[3]s" | chpasswd
printf '%%s\n' %[4]s > /etc/pve/priv/authorized_keys
echo "$node" > %[5]s
systemd-run --on-active=2 systemctl reboot >/dev/null
`, node, fqdn, password, shellQuote(authorizedKeys), IdentityMarker)
}

// Finish removes what is left of the base image's identity after the reboot and
// checks that Proxmox created the node's certificate.
func Finish(node string) string {
	return fmt.Sprintf(`
[ "$(hostname)" = %[1]q ] || { echo "host name is $(hostname)" >&2; exit 1; }
rm -rf /etc/pve/nodes/%[2]s
for i in $(seq 30); do [ -f /etc/pve/nodes/%[1]s/pve-ssl.pem ] && exit 0; sleep 2; done
echo "no certificate for %[1]s" >&2; exit 1
`, node, Hostname)
}

// shellQuote returns the words single-quoted for a shell command line.
func shellQuote(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}
