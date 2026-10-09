package qemu

import (
	"flag"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/virtbase/proxbase/internal/vm"
)

var update = flag.Bool("update", false, "update golden files")

func spec() vm.Spec {
	return vm.Spec{
		Name: "pve1", Process: "lab-pve1", CPUs: 4, MemoryMiB: 4096, Nested: true,
		RootDisk: vm.Disk{Path: "/d/pve1-root.qcow2"}, DataDisks: []vm.Disk{{Path: "/d/pve1-data0.qcow2"}},
		NATMAC: vm.MAC(1, 0), Bind: "127.0.0.1", UIPort: 18001, SSHPort: 18101,
		NICs:   []vm.NIC{{MAC: vm.MAC(1, 1), Local: "/r/pve1-cluster.sock", Remote: "/r/sw-cluster.sock"}},
		RunDir: "/r", LogDir: "/l",
	}
}

func golden(t *testing.T, name string, args []string) {
	t.Helper()
	got := strings.Join(args, "\n") + "\n"
	path := "testdata/" + name
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s differs (run go test -update):\n%s", name, got)
	}
}

func TestRunArgs(t *testing.T) { golden(t, "run.args", args(spec(), nil)) }

func TestInstallArgs(t *testing.T) {
	s := spec()
	s.Nested = false
	golden(t, "install.args", args(s, &vm.Media{ISO: "/c/pve.iso", Kernel: "/c/linux26", Initrd: "/c/initrd.img", Cmdline: "ro quiet console=ttyS0,115200", AnswerDir: "/d/ans"}))
}

// Devices shared by install and run must sit at the same PCI addresses.
func TestStablePCI(t *testing.T) {
	run := devices(args(spec(), nil))
	inst := devices(args(spec(), &vm.Media{}))
	for _, d := range run {
		if strings.Contains(d, "addr=") && !slices.Contains(inst, d) {
			t.Errorf("device %q missing or moved in install mode", d)
		}
	}
}

func devices(args []string) []string {
	var out []string
	for i, a := range args {
		if a == "-device" {
			out = append(out, args[i+1])
		}
	}
	return out
}
