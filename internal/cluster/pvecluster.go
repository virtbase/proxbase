package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/virtbase/proxbase/internal/config"
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
	for _, n := range nodes[1:] {
		if err := c.join(ctx, api, first, n, fp); err != nil {
			return err
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

// join adds node n unless it is a member already.
func (c *Cluster) join(ctx context.Context, api *pve.Client, first, n config.Node, fp string) error {
	_, members, err := api.ClusterStatus(ctx)
	if err != nil {
		return err
	}
	if _, ok := members[n.Name]; ok {
		return nil
	}
	form := c.links(n)
	c.step("joining %s (%s)", n.Name, describeLinks(form))
	napi, err := c.api(ctx, n.Name)
	if err != nil {
		return err
	}
	form.Set("hostname", c.corosyncIP(first))
	form.Set("password", c.password)
	form.Set("fingerprint", fp)
	// The cluster refuses joins until it is quorate.
	if err := waitMember(ctx, api, first.Name, nil, "", 2*time.Minute); err != nil {
		return err
	}
	var upid string
	if err := napi.Post(ctx, "/cluster/config/join", form, &upid); err != nil {
		return fmt.Errorf("join %s: %w", n.Name, err)
	}
	return waitMember(ctx, api, n.Name, napi, upid, 3*time.Minute)
}

// waitMember waits until node is listed online in a quorate cluster. If a join
// task is given, its failure ends the wait early.
func waitMember(ctx context.Context, api *pve.Client, node string, joiner *pve.Client, upid string, timeout time.Duration) error {
	var joinErr error
	err := retry.Do(ctx, timeout, 2*time.Second, func() error {
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

var (
	votesRe   = regexp.MustCompile(`Total votes:\s+(\d+)`)
	quorateRe = regexp.MustCompile(`Quorate:\s+Yes`)
)

// waitQuorum waits until every node is quorate, sees all votes and can write /etc/pve.
func (c *Cluster) waitQuorum(ctx context.Context, timeout time.Duration) error {
	want := c.Cfg.Nodes.Count
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range c.Cfg.NodeList() {
		g.Go(func() error {
			var last string
			err := retry.Do(gctx, timeout, 2*time.Second, func() error {
				s, err := c.waitSSH(gctx, n.Name, time.Minute)
				if err != nil {
					return retry.Permanent(err)
				}
				out, err := s.Run(`pvecm status 2>&1 || true
f=/etc/pve/.proxbase-write-test; if echo ok > $f 2>/dev/null; then rm -f $f; echo WRITABLE; fi`)
				s.Close()
				m := votesRe.FindStringSubmatch(out)
				if err == nil && quorateRe.MatchString(out) && m != nil && m[1] == strconv.Itoa(want) && strings.Contains(out, "WRITABLE") {
					return nil
				}
				last = out
				return errors.New("no quorum")
			})
			if err != nil && gctx.Err() == nil && last != "" {
				return fmt.Errorf("%s: no quorum after %s:\n%s", n.Name, timeout, last)
			}
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	c.step("cluster quorate: %d/%d votes, /etc/pve writable on every node", want, want)
	return nil
}
