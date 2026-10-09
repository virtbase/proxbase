package qemu

import (
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

func machine() *Machine {
	return &Machine{
		Name: "lab-pve1", CPUs: 4, MemoryMiB: 4096, Nested: true,
		RootDisk: "/d/pve1-root.qcow2", DataDisks: []string{"/d/pve1-data0.qcow2"},
		NATMAC: MAC(1, 0), Bind: "127.0.0.1", UIPort: 18001, SSHPort: 18101,
		NICs: []NIC{{MAC: MAC(1, 1), Local: "/r/pve1-cluster.sock", Switch: "/r/sw-cluster.sock"}},
		QMP:  "/r/pve1.qmp", PIDFile: "/r/pve1.pid", Console: "/r/pve1-console.sock", ConsoleLog: "/l/pve1-console.log",
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

func TestRunArgs(t *testing.T) { golden(t, "run.args", machine().Args()) }

func TestInstallArgs(t *testing.T) {
	m := machine()
	m.Nested = false
	m.Install = &Install{ISO: "/c/pve.iso", Kernel: "/c/linux26", Initrd: "/c/initrd.img", Cmdline: "ro quiet console=ttyS0,115200", AnswerDir: "/d/ans", SerialSock: "/r/serial.sock"}
	golden(t, "install.args", m.Args())
}

// Devices shared by install and run must sit at the same PCI addresses.
func TestStablePCI(t *testing.T) {
	m := machine()
	run := devices(m.Args())
	m.Install = &Install{}
	inst := devices(m.Args())
	for _, d := range run {
		if strings.Contains(d, "addr=") && !contains(inst, d) {
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

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
