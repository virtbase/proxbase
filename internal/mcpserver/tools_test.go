package mcpserver

import (
	"testing"
	"time"
)

func TestBeforeStatus(t *testing.T) {
	tests := []struct {
		name        string
		limit, want time.Duration
	}{
		{name: "no limit", limit: 0, want: 0},
		{name: "short client limit", limit: shortCallLimit, want: shortCallLimit - statusMargin},
		{name: "tiny configured limit", limit: 4 * time.Second, want: 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := beforeStatus(tt.limit); got != tt.want {
				t.Errorf("beforeStatus(%s) = %s, want %s", tt.limit, got, tt.want)
			}
		})
	}
}
