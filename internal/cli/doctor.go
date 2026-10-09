package cli

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/qemu"
	"github.com/virtbase/proxbase/internal/state"
)

type check struct {
	name, status, detail string
}

func doctorCmd() *cobra.Command {
	var portBase int
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that this host can run proxbase clusters",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			checks := []check{checkKVM(), checkNested(), checkQEMU(), checkQEMUImg(), checkMemory(), checkDisk(), checkPorts(portBase), checkSocketPath()}
			failed := false
			for _, c := range checks {
				fmt.Printf("%-5s %-12s %s\n", c.status, c.name, c.detail)
				failed = failed || c.status == "FAIL"
			}
			if failed {
				return fmt.Errorf("some checks failed")
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&portBase, "port-base", 18000, "access.portBase to check")
	return cmd
}

func ok(name, detail string) check   { return check{name, "OK", detail} }
func warn(name, detail string) check { return check{name, "WARN", detail} }
func fail(name, detail string) check { return check{name, "FAIL", detail} }

func checkKVM() check {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return fail("kvm", fmt.Sprintf("%v (load kvm_intel/kvm_amd and add yourself to group kvm)", err))
	}
	f.Close()
	return ok("kvm", "/dev/kvm is read/writable")
}

func checkNested() check {
	for _, mod := range []string{"kvm_amd", "kvm_intel"} {
		b, err := os.ReadFile("/sys/module/" + mod + "/parameters/nested")
		if err != nil {
			continue
		}
		v := strings.TrimSpace(string(b))
		if v == "1" || v == "Y" {
			return ok("nested", mod+" nested=1")
		}
		return warn("nested", fmt.Sprintf("%s nested=%s: guests inside the nodes cannot use KVM (options %s nested=1)", mod, v, mod))
	}
	return warn("nested", "kvm_amd/kvm_intel not loaded")
}

var qemuVersionRe = regexp.MustCompile(`version (\d+)\.(\d+)`)

func checkQEMU() check {
	out, err := exec.Command(qemu.Binary, "--version").Output()
	if err != nil {
		return fail("qemu", qemu.Binary+" not found (install qemu-system-x86)")
	}
	m := qemuVersionRe.FindStringSubmatch(string(out))
	if m == nil {
		return warn("qemu", "unknown version: "+firstLine(string(out)))
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < 7 || (major == 7 && minor < 2) {
		return fail("qemu", fmt.Sprintf("QEMU %s.%s is too old; -netdev dgram needs 7.2+", m[1], m[2]))
	}
	return ok("qemu", firstLine(string(out)))
}

func checkQEMUImg() check {
	if _, err := exec.LookPath("qemu-img"); err != nil {
		return fail("qemu-img", "not found (install qemu-utils)")
	}
	return ok("qemu-img", "found")
}

func checkMemory() check {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return warn("memory", err.Error())
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var kb int
		if n, _ := fmt.Sscanf(sc.Text(), "MemAvailable: %d kB", &kb); n == 1 {
			gib := float64(kb) / (1 << 20)
			detail := fmt.Sprintf("%.1f GiB available (default node: 4 GiB)", gib)
			if gib < 12 {
				return warn("memory", detail+"; a 3-node cluster needs about 12 GiB")
			}
			return ok("memory", detail)
		}
	}
	return warn("memory", "MemAvailable not found")
}

func checkDisk() check {
	dir := state.DataHome()
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		dir = dir[:strings.LastIndex(dir, "/")]
		if dir == "" {
			dir = "/"
		}
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return warn("disk", err.Error())
	}
	gib := float64(st.Bavail) * float64(st.Bsize) / (1 << 30)
	detail := fmt.Sprintf("%.0f GiB free in %s (about 4 GiB per node + 1.7 GiB ISO cache)", gib, dir)
	if gib < 20 {
		return warn("disk", detail)
	}
	return ok("disk", detail)
}

func checkPorts(base int) check {
	var busy []string
	for _, p := range []int{base + 1, base + 2, base + 3, base + 101, base + 102, base + 103} {
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err != nil {
			busy = append(busy, strconv.Itoa(p))
			continue
		}
		l.Close()
	}
	if len(busy) > 0 {
		return warn("ports", "in use: "+strings.Join(busy, ", ")+" (set access.portBase, or a cluster is running)")
	}
	return ok("ports", fmt.Sprintf("127.0.0.1:%d-%d and %d-%d free", base+1, base+3, base+101, base+103))
}

func checkSocketPath() check {
	p := state.ForCluster("abcdefghijklmno").Run("pve10-network12.sock")
	if len(p) > 107 {
		return fail("paths", fmt.Sprintf("%s is %d bytes, unix sockets allow 107; use a shorter XDG_DATA_HOME", state.DataHome(), len(p)))
	}
	return ok("paths", "state in "+state.DataHome()+", ISO cache in "+state.CacheHome())
}
