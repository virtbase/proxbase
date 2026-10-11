package golden

import (
	"strings"
	"testing"

	"github.com/virtbase/proxbase/internal/config"
)

func TestKey(t *testing.T) {
	cfg, _ := config.Decode(strings.NewReader("nodes: {count: 2, overrides: {pve2: {rootDisk: {size: 64G}}}}\n"))
	cfg.SetDefaults()
	n := cfg.NodeList()
	k1, k2 := Key(cfg, n[0], "/c/proxmox-ve_9.2-1.iso"), Key(cfg, n[1], "/c/proxmox-ve_9.2-1.iso")
	if k1 == k2 || len(k1) != 16 {
		t.Fatalf("different root sizes need different keys: %s %s", k1, k2)
	}
	if Key(cfg, n[0], "/other/proxmox-ve_9.2-1.iso") != k1 {
		t.Fatal("the ISO directory must not change the key")
	}
	if Key(cfg, n[0], "/c/proxmox-ve_9.2-2.iso") == k1 {
		t.Fatal("a different ISO needs a different key")
	}
	ceph, _ := config.Decode(strings.NewReader("nodes: {count: 3}\nstorage: {ceph: {enabled: true}}\n"))
	ceph.SetDefaults()
	if !ceph.CephEnabled() {
		t.Fatal("test config must enable Ceph")
	}
	if Key(ceph, ceph.NodeList()[0], "/c/proxmox-ve_9.2-1.iso") == k1 {
		t.Fatal("a base image with Ceph baked in needs a different key")
	}
}

func TestPersonalize(t *testing.T) {
	s := Personalize("pve2", "lab.internal", "pw123", []string{"ssh-ed25519 AAAA a'b"})
	for _, want := range []string{
		"node=pve2 fqdn=pve2.lab.internal",
		"10.0.2.15 $fqdn $node",
		"ssh-keygen -A",
		"systemd-machine-id-setup",
		`printf '%s\n' 'ssh-ed25519 AAAA a'\''b' > /etc/pve/priv/authorized_keys`,
		"echo \"$node\" > " + IdentityMarker,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if !strings.Contains(Finish("pve2"), "rm -rf /etc/pve/nodes/golden") {
		t.Error("Finish must remove the base image's node directory")
	}
}

func TestLastLines(t *testing.T) {
	console := "\x1b[H\x1b[J\x1b[1;1H\x1b[?25l\x1b[m  GNU GRUB  version 2.12\r\n\x1b[5;78H\r\n" +
		"  Booting `Proxmox VE GNU/Linux'\r\n\r\n\x1b]104\x07\x1b[?7herror: Another instance of this program is already running.\r\n" +
		"proxbase-first-boot: bake failed\r\n"
	got := lastLines(console, 2)
	want := "  error: Another instance of this program is already running.\n  proxbase-first-boot: bake failed"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
