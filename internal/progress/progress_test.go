package progress

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	var b bytes.Buffer
	s := Text(&b)
	s(Event{Type: TypeStep, Message: "pve1 installed", Elapsed: 42.4})
	s.Logf("warning: %s", "low memory")
	s(Event{Type: TypeDone})
	s(Event{Type: TypeError, Error: "boom", Hint: "re-run"})
	want := "[   42s] pve1 installed\nwarning: low memory\nError: boom\n\nre-run\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestJSON(t *testing.T) {
	var b bytes.Buffer
	s := JSON(&b)
	s(Event{Type: TypeStep, Cluster: "lab", Node: "pve1", Message: "pve1 installed", Elapsed: 1.5})
	s(Event{Type: TypeError, Error: "boom", Hint: "re-run", Log: "/x/logs"})
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", b.String())
	}
	var e Event
	if err := json.Unmarshal([]byte(lines[1]), &e); err != nil || e.Error != "boom" || e.Log != "/x/logs" {
		t.Errorf("error event %q: %v", lines[1], err)
	}
	if !strings.Contains(lines[0], `"node":"pve1"`) || strings.Contains(lines[0], `"error"`) {
		t.Errorf("step event %s", lines[0])
	}
}
