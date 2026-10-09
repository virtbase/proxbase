package image

import "testing"

const grub = `menuentry 'Install Proxmox VE (Automated)' --class debian {
        echo        'Loading Proxmox VE Automatic Installer ...'
        linux       /boot/linux26 ro ramdisk_size=16777216 rw quiet splash=silent proxmox-start-auto-installer
        initrd      /boot/initrd.img
}`

func TestCmdline(t *testing.T) {
	got, err := Cmdline(grub)
	want := "ro ramdisk_size=16777216 rw quiet proxmox-start-auto-installer console=ttyS0,115200"
	if err != nil || got != want {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := Cmdline("linux /boot/linux26 ro quiet"); err == nil {
		t.Fatal("expected error without auto-installer entry")
	}
}

func TestResolve(t *testing.T) {
	sums := ParseSums(`aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  proxmox-ve_9.2-1.iso
bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  proxmox-ve_9.2-2.iso
cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc  proxmox-ve_9.1-1.iso
dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd  proxmox-backup-server_4.0-1.iso
`)
	for v, want := range map[string]string{"9.2": "proxmox-ve_9.2-2.iso", "9.2-1": "proxmox-ve_9.2-1.iso", "9.1": "proxmox-ve_9.1-1.iso"} {
		if f, _, err := resolve(sums, v); err != nil || f != want {
			t.Errorf("%s: got %s %v", v, f, err)
		}
	}
	if _, _, err := resolve(sums, "8.4"); err == nil {
		t.Error("expected error for missing version")
	}
}
