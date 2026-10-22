package doctor

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestCheckPorts(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	busy := l.Addr().(*net.TCPAddr).Port
	c := checkPorts(busy - 1) // the first UI port is taken
	if c.Status != StatusWarn || !strings.Contains(c.Detail, strconv.Itoa(busy)) {
		t.Errorf("checkPorts with port %d in use = %+v", busy, c)
	}
}

func TestFailed(t *testing.T) {
	if Failed([]Check{ok("a", ""), warn("b", "")}) {
		t.Error("warnings counted as failure")
	}
	if !Failed([]Check{ok("a", ""), fail("b", "")}) {
		t.Error("failure not reported")
	}
}
