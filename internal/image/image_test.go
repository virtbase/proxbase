package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// An exact version that is cached and verified must not touch the mirror.
func TestEnsure_CachedExactVersionOffline(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	dir := filepath.Join(cache, "iso")
	boot := filepath.Join(dir, "proxmox-ve_9.2-1")
	if err := os.MkdirAll(boot, 0o755); err != nil {
		t.Fatal(err)
	}
	iso := filepath.Join(dir, "proxmox-ve_9.2-1.iso")
	files := map[string]string{
		iso:                               "iso",
		iso + ".sha256":                   strings.Repeat("a", 64) + " 3\n",
		filepath.Join(boot, "linux26"):    "k",
		filepath.Join(boot, "initrd.img"): "i",
		filepath.Join(boot, "grub.cfg"):   grub,
	}
	for p, content := range files {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	img, err := Ensure(t.Context(), "http://127.0.0.1:1", "9.2-1", cache, t.Logf)
	if err != nil {
		t.Fatalf("Ensure went to the (unreachable) mirror: %v", err)
	}
	if img.Version != "9.2-1" || img.ISO != iso {
		t.Fatalf("got %+v", img)
	}
}

func TestVerified(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		stamp string
		want  bool
	}{
		{name: "matching size", stamp: strings.Repeat("a", 64) + " 3\n", want: true},
		{name: "size changed", stamp: strings.Repeat("a", 64) + " 4\n", want: false},
		{name: "no stamp", stamp: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "x.iso")
			if err := os.WriteFile(p, []byte("iso"), 0o644); err != nil {
				t.Fatal(err)
			}
			if tt.stamp != "" {
				if err := os.WriteFile(p+".sha256", []byte(tt.stamp), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := verified(p); got != tt.want {
				t.Errorf("verified = %v, want %v", got, tt.want)
			}
		})
	}
}
