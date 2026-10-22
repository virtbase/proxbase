package state

import (
	"os"
	"testing"
	"time"

	"github.com/virtbase/proxbase/internal/config"
)

// Cluster files written before automatic port bases stored the default.
func TestLoadConfigPortBase(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tests := []struct {
		name string
		file string
		want int
	}{
		{name: "without portBase", file: "name: old\n", want: config.DefaultPortBase},
		{name: "with portBase", file: "name: new\naccess:\n  portBase: 21000\n", want: 21000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := ForCluster("c" + string(rune('a'+len(tt.name)%26)))
			if err := d.Init(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(d.Config(), []byte(tt.file), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := d.LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Access.PortBase != tt.want {
				t.Errorf("port base %d, want %d", cfg.Access.PortBase, tt.want)
			}
		})
	}
}

func TestExpired(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Minute), now.Add(time.Minute)
	for _, tt := range []struct {
		name    string
		expires *time.Time
		want    bool
	}{{"never", nil, false}, {"past", &past, true}, {"future", &future, false}} {
		t.Run(tt.name, func(t *testing.T) {
			if got := (&State{ExpiresAt: tt.expires}).Expired(now); got != tt.want {
				t.Errorf("Expired = %v, want %v", got, tt.want)
			}
		})
	}
}
