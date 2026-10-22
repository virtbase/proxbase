package cli

import (
	"testing"
	"time"

	"github.com/virtbase/proxbase/internal/cluster"
)

func TestExpiry(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct{ expires, want string }{
		{"", "-"},
		{"2026-10-22T13:30:00Z", "in 1h30m0s"},
		{"2026-10-22T11:00:00Z", "expired"},
		{"2026-10-22T12:00:20Z", "in 20s"},
	} {
		if got := expiry(&cluster.Status{Expires: tt.expires}, now); got != tt.want {
			t.Errorf("expiry(%q) = %q, want %q", tt.expires, got, tt.want)
		}
	}
}
