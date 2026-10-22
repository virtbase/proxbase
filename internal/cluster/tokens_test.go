package cluster

import "testing"

func TestCheckTokenName(t *testing.T) {
	for _, tt := range []struct {
		name string
		ok   bool
	}{
		{"readonly", true},
		{"vm-100", true},
		{"api", false},
		{"Upper", false},
		{"1st", false},
		{"with_underscore", false},
		{"a-very-long-token-name-x", false},
		{"", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := CheckTokenName(tt.name); (err == nil) != tt.ok {
				t.Errorf("CheckTokenName(%q) = %v, want ok=%v", tt.name, err, tt.ok)
			}
		})
	}
}
