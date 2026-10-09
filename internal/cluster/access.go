package cluster

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/virtbase/proxbase/internal/pve"
)

// ensureToken creates the API token proxbase@pve!api (Administrator on /) unless
// the stored one still works.
func (c *Cluster) ensureToken(ctx context.Context) error {
	if !*c.Cfg.Access.APIToken {
		return nil
	}
	api, err := c.api(ctx, c.Cfg.NodeList()[0].Name)
	if err != nil {
		return err
	}
	if tok, err := c.Token(); err == nil && tok != "" {
		probe := *api
		probe.SetToken(tok)
		if probe.Get(ctx, "/version", &struct{}{}) == nil {
			return nil
		}
	}
	var users []struct {
		UserID string `json:"userid"`
	}
	if err := api.Get(ctx, "/access/users", &users); err != nil {
		return err
	}
	if !slices.ContainsFunc(users, func(u struct {
		UserID string `json:"userid"`
	}) bool {
		return u.UserID == TokenUser
	}) {
		if err := api.Post(ctx, "/access/users", url.Values{"userid": {TokenUser}, "comment": {"proxbase"}}, nil); err != nil {
			return fmt.Errorf("create user %s: %w", TokenUser, err)
		}
	}
	if err := api.Put(ctx, "/access/acl", url.Values{"path": {"/"}, "roles": {"Administrator"}, "users": {TokenUser}}, nil); err != nil {
		return fmt.Errorf("grant Administrator to %s: %w", TokenUser, err)
	}
	tokenPath := "/access/users/" + url.PathEscape(TokenUser) + "/token/api"
	if err := api.Delete(ctx, tokenPath); err != nil && !pve.IsStatus(err, 500) && !pve.IsStatus(err, 404) {
		return err
	}
	var tok struct {
		FullID string `json:"full-tokenid"`
		Value  string `json:"value"`
	}
	if err := api.Post(ctx, tokenPath, url.Values{"privsep": {"0"}, "comment": {"proxbase"}}, &tok); err != nil {
		return fmt.Errorf("create API token: %w", err)
	}
	c.step("API token %s created", tok.FullID)
	return c.Dir.WriteSecret(secretToken, []byte(tok.FullID+"="+tok.Value+"\n"))
}

// exportCA saves the cluster CA certificate for clients.
func (c *Cluster) exportCA(ctx context.Context) error {
	s, err := c.waitSSH(ctx, c.Cfg.NodeList()[0].Name, time.Minute)
	if err != nil {
		return err
	}
	defer s.Close()
	ca, err := s.Run("cat /etc/pve/pve-root-ca.pem")
	if err != nil {
		return err
	}
	return c.Dir.WriteSecret(secretCA, []byte(ca))
}
