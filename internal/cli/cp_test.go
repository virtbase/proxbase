package cli

import "testing"

func TestParseNodeFile(t *testing.T) {
	tests := []struct {
		in     string
		want   nodeFile
		onNode bool
	}{
		{in: "pve1:/root/a.yml", want: nodeFile{node: "pve1", path: "/root/a.yml"}, onNode: true},
		{in: "lab:pve2:/etc/pve/storage.cfg", want: nodeFile{cluster: "lab", node: "pve2", path: "/etc/pve/storage.cfg"}, onNode: true},
		{in: "lab:pve1:/srv/a:b", want: nodeFile{cluster: "lab", node: "pve1", path: "/srv/a:b"}, onNode: true},
		{in: "./playbook.yml"},
		{in: "/tmp/a:/b"},
		{in: "-"},
		{in: "relative/x:/y"},
		{in: ":/root/a"},
		{in: "a:b:c:/x"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, onNode := parseNodeFile(tt.in)
			if onNode != tt.onNode || (onNode && got != tt.want) {
				t.Errorf("parseNodeFile(%q) = %+v, %v; want %+v, %v", tt.in, got, onNode, tt.want, tt.onNode)
			}
		})
	}
}
