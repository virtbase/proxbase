// Package answer renders the answer.toml for the Proxmox automated installer.
package answer

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

type Params struct {
	FQDN         string
	Mailto       string
	Keyboard     string
	Country      string
	Timezone     string
	RootPassword string
	SSHKeys      []string
	MAC          string // MAC of the NAT NIC; only this NIC gets the static install-time address
	Filesystem   string // zfs, ext4, xfs, btrfs
}

// Render returns answer.toml. The NAT NIC uses QEMU user networking (10.0.2.0/24).
func Render(p Params) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	keys := make([]string, len(p.SSHKeys))
	for i, k := range p.SSHKeys {
		keys[i] = quote(strings.TrimSpace(k))
	}
	w("[global]")
	w("keyboard = %s", quote(p.Keyboard))
	w("country = %s", quote(p.Country))
	w("fqdn = %s", quote(p.FQDN))
	w("mailto = %s", quote(p.Mailto))
	w("timezone = %s", quote(p.Timezone))
	w("root-password = %s", quote(p.RootPassword))
	w("root-ssh-keys = [%s]", strings.Join(keys, ", "))
	w("reboot-mode = \"power-off\"")
	w("")
	w("[network]")
	w("source = \"from-answer\"")
	w("cidr = \"10.0.2.15/24\"")
	w("gateway = \"10.0.2.2\"")
	w("dns = \"10.0.2.3\"")
	w("filter.ID_NET_NAME_MAC = %s", quote("*"+strings.ReplaceAll(strings.ToLower(p.MAC), ":", "")))
	w("")
	w("[disk-setup]")
	w("filesystem = %s", quote(p.Filesystem))
	w("disk-list = [\"vda\"]")
	switch p.Filesystem {
	case "zfs":
		w("zfs.raid = \"raid0\"")
	case "btrfs":
		w("btrfs.raid = \"raid0\"")
	}
	return b.String()
}

// quote returns a TOML basic string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Password returns a random alphanumeric password.
func Password(n int) string {
	const chars = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, n)
	for i := range out {
		v, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			panic(err)
		}
		out[i] = chars[v.Int64()]
	}
	return string(out)
}
