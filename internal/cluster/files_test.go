package cluster

import (
	"os"
	"testing"
)

func TestCheckNodePath(t *testing.T) {
	tests := []struct {
		path string
		ok   bool
	}{
		{"/root/a.yml", true},
		{"/etc/pve/storage.cfg", true},
		{"root/a", false},
		{"/root/../etc/x", false},
		{"/root//a", false},
		{"/root/a/", false},
		{"/", false},
		{"/a\x00b", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if err := CheckNodePath(tt.path); (err == nil) != tt.ok {
				t.Errorf("CheckNodePath(%q) = %v, want ok=%v", tt.path, err, tt.ok)
			}
		})
	}
}

func TestParseFileMode(t *testing.T) {
	tests := []struct {
		in   string
		want os.FileMode
		ok   bool
	}{
		{"0644", 0o644, true},
		{"755", 0o755, true},
		{"0600", 0o600, true},
		{"4755", 0, false},
		{"rw-r--r--", 0, false},
		{"", 0, false},
		{"0999", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseFileMode(tt.in)
			if (err == nil) != tt.ok || got != tt.want {
				t.Errorf("ParseFileMode(%q) = %o, %v; want %o, ok=%v", tt.in, got, err, tt.want, tt.ok)
			}
		})
	}
}
