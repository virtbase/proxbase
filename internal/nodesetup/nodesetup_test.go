package nodesetup

import (
	"strings"
	"testing"

	"github.com/virtbase/proxbase/internal/config"
)

func TestScript(t *testing.T) {
	cfg, err := config.Decode(strings.NewReader(`
nodes: {count: 2}
networks:
  - {name: cluster, cidr: 10.10.10.0/24, roles: [corosync]}
  - {name: storage, cidr: 10.10.20.0/24, mtu: 9000}
  - {name: guests, vlanAware: true, vlans: ["100", "200-299"]}
`))
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetDefaults()
	s := Script(cfg, cfg.NodeList()[1])
	for _, want := range []string{
		"10.10.10.11 pve1.proxbase.internal pve1\n10.10.10.12 pve2.proxbase.internal pve2\n",
		"nic0=$(ifname 52:54:00:50:02:01)\nnic1=$(ifname 52:54:00:50:02:02)\nnic2=$(ifname 52:54:00:50:02:03)\n",
		"auto vmbr1\niface vmbr1 inet static\n\taddress 10.10.10.12/24\n\tbridge-ports $nic0\n",
		"auto vmbr2\niface vmbr2 inet static\n\taddress 10.10.20.12/24\n\tbridge-ports $nic1\n\tbridge-stp off\n\tbridge-fd 0\n\tmtu 9000\n",
		"auto vmbr3\niface vmbr3 inet manual\n\tbridge-ports $nic2\n\tbridge-stp off\n\tbridge-fd 0\n\tbridge-vlan-aware yes\n\tbridge-vids 100 200-299\n",
		"Components: pve-no-subscription",
		"serial-getty@ttyS0",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
}
