package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/nodesetup"
	"github.com/virtbase/proxbase/internal/pve"
	"github.com/virtbase/proxbase/internal/retry"
)

// links returns the corosync link parameters (link0, link1) of a node.
func (c *Cluster) links(n config.Node) url.Values {
	v := url.Values{}
	for i, net := range c.Cfg.CorosyncNetworks() {
		v.Set(fmt.Sprintf("link%d", i), nodeIP(n, net))
	}
	return v
}

func describeLinks(v url.Values) string {
	s := "link0 " + v.Get("link0")
	if l1 := v.Get("link1"); l1 != "" {
		s += ", link1 " + l1
	}
	return s
}

// formCluster creates the cluster on the first node and joins the others via the API.
func (c *Cluster) formCluster(ctx context.Context) error {
	nodes := c.Cfg.NodeList()
	first := nodes[0]
	api, err := c.api(ctx, first.Name)
	if err != nil {
		return err
	}
	cs, _, err := api.ClusterStatus(ctx)
	if err != nil {
		return err
	}
	if cs == nil {
		form := c.links(first)
		form.Set("clustername", c.Cfg.Name)
		c.step("creating cluster %s on %s (%s)", c.Cfg.Name, first.Name, describeLinks(form))
		if err := api.PostTask(ctx, first.Name, "/cluster/config", form, 2*time.Minute); err != nil {
			return fmt.Errorf("create cluster: %w", err)
		}
		// pmxcfs restarts; log in again.
		if api, err = c.api(ctx, first.Name); err != nil {
			return err
		}
	}
	fp, err := c.fingerprint(ctx, first.Name)
	if err != nil {
		return err
	}
	joiners, err := c.joinClients(ctx, api, nodes[1:])
	if err != nil {
		return err
	}
	// Joins run one at a time: concurrent joins race for the cluster lock.
	members := len(nodes) - len(joiners)
	for _, n := range nodes[1:] {
		if napi, ok := joiners[n.Name]; ok {
			if err := c.join(ctx, api, napi, first, n, fp, members); err != nil {
				return err
			}
			members++
		}
	}
	if m, ok := c.Cfg.RoleNetwork(config.RoleMigration); ok {
		if err := api.Put(ctx, "/cluster/options", url.Values{"migration": {"type=secure,network=" + m.CIDR}}, nil); err != nil {
			return fmt.Errorf("set migration network: %w", err)
		}
	}
	return nil
}

// fingerprint returns the certificate fingerprint of a cluster member.
func (c *Cluster) fingerprint(ctx context.Context, node string) (string, error) {
	s, err := c.waitSSH(ctx, node, time.Minute)
	if err != nil {
		return "", err
	}
	defer s.Close()
	certPEM, err := s.Run("cat /etc/pve/nodes/" + node + "/pve-ssl.pem")
	if err != nil {
		return "", err
	}
	return pve.Fingerprint(certPEM)
}

// joinClients logs in, in parallel, to the nodes that are not members yet.
func (c *Cluster) joinClients(ctx context.Context, api *pve.Client, nodes []config.Node) (map[string]*pve.Client, error) {
	_, members, err := api.ClusterStatus(ctx)
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	out := map[string]*pve.Client{}
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range nodes {
		if _, ok := members[n.Name]; ok {
			continue
		}
		g.Go(func() error {
			napi, err := c.api(gctx, n.Name)
			if err != nil {
				return err
			}
			mu.Lock()
			out[n.Name] = napi
			mu.Unlock()
			return nil
		})
	}
	return out, g.Wait()
}

// join adds node n to the cluster through its own API client napi. members is
// the number of nodes in the cluster before the join.
func (c *Cluster) join(ctx context.Context, api, napi *pve.Client, first, n config.Node, fp string, members int) error {
	form := c.links(n)
	c.step("joining %s (%s)", n.Name, describeLinks(form))
	form.Set("hostname", c.corosyncIP(first))
	form.Set("password", c.password)
	form.Set("fingerprint", fp)
	for attempt := 1; ; attempt++ {
		// The cluster refuses joins until it is quorate and /etc/pve is writable.
		if err := c.waitNodeQuorate(ctx, first.Name, members, 2*time.Minute); err != nil {
			return err
		}
		var upid string
		if err := napi.Post(ctx, "/cluster/config/join", form, &upid); err != nil {
			return fmt.Errorf("join %s: %w", n.Name, err)
		}
		err := waitMember(ctx, api, n.Name, napi, upid, 3*time.Minute)
		// A join refused for lack of quorum changed nothing; try again.
		if err == nil || attempt == 3 || !strings.Contains(err.Error(), "no quorum") {
			return err
		}
		c.step("%s: join refused (the cluster had no quorum yet), retrying", n.Name)
	}
}

// waitMember waits until node is listed online in a quorate cluster. If a join
// task is given, its failure ends the wait early.
func waitMember(ctx context.Context, api *pve.Client, node string, joiner *pve.Client, upid string, timeout time.Duration) error {
	var joinErr error
	err := retry.Do(ctx, timeout, time.Second, func() error {
		cs, members, err := api.ClusterStatus(ctx)
		if err == nil && cs != nil && cs.Quorate == 1 && members[node].Online == 1 {
			return nil
		}
		if joiner != nil {
			if done, terr := joiner.TaskDone(ctx, node, upid); done && terr != nil {
				joinErr = fmt.Errorf("join %s: %w", node, terr)
				return retry.Permanent(joinErr)
			}
		}
		if err == nil {
			err = errors.New("not online yet")
		}
		return err
	})
	if err != nil && joinErr == nil && ctx.Err() == nil {
		return fmt.Errorf("%s did not come online in the cluster within %s: %w", node, timeout, err)
	}
	return err
}

// repairJoins repairs what racing cluster joins left broken (see
// nodesetup.RepairJoin): without it a node's API may have no certificate, one the
// cluster CA does not vouch for, or reject every login.
func (c *Cluster) repairJoins(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range c.Cfg.NodeList() {
		g.Go(func() error {
			s, err := c.waitSSH(gctx, n.Name, time.Minute)
			if err != nil {
				return err
			}
			defer s.Close()
			out, err := s.Run(nodesetup.RepairJoin)
			if err != nil {
				return fmt.Errorf("%s: repair join: %w", n.Name, err)
			}
			if strings.Contains(out, "repaired") {
				c.step("%s: the cluster join left the node certificate missing or from another CA; issued a new one", n.Name)
			}
			return nil
		})
	}
	return g.Wait()
}

var (
	votesRe   = regexp.MustCompile(`Total votes:\s+(\d+)`)
	quorateRe = regexp.MustCompile(`Quorate:\s+Yes`)
)

// waitQuorum waits until every node is quorate, sees all votes and can write /etc/pve.
func (c *Cluster) waitQuorum(ctx context.Context, timeout time.Duration) error {
	want := c.Cfg.Nodes.Count
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range c.Cfg.NodeList() {
		g.Go(func() error { return c.waitNodeQuorate(gctx, n.Name, want, timeout) })
	}
	if err := g.Wait(); err != nil {
		return err
	}
	c.step("cluster quorate: %d/%d votes, /etc/pve writable on every node", want, want)
	return nil
}

// waitNodeQuorate waits until node is quorate with votes votes and can write
// /etc/pve. The API reports quorum a moment before the cluster file system
// accepts changes such as a join.
func (c *Cluster) waitNodeQuorate(ctx context.Context, node string, votes int, timeout time.Duration) error {
	var last string
	err := retry.Do(ctx, timeout, time.Second, func() error {
		s, err := c.waitSSH(ctx, node, time.Minute)
		if err != nil {
			return retry.Permanent(err)
		}
		out, err := s.Run(`pvecm status 2>&1 || true
f=/etc/pve/.proxbase-write-test; if echo ok > $f 2>/dev/null; then rm -f $f; echo WRITABLE; fi`)
		s.Close()
		m := votesRe.FindStringSubmatch(out)
		if err == nil && quorateRe.MatchString(out) && m != nil && m[1] == strconv.Itoa(votes) && strings.Contains(out, "WRITABLE") {
			return nil
		}
		last = out
		return errors.New("no quorum")
	})
	if err != nil && ctx.Err() == nil && last != "" {
		return fmt.Errorf("%s: no quorum after %s:\n%s", node, timeout, last)
	}
	return err
}
