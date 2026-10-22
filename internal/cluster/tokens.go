package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/virtbase/proxbase/internal/pve"
)

// mainToken is the name of the token every cluster gets (TokenUser!api).
const mainToken = "api"

var tokenNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)

// ScopedToken is an API token of TokenUser with privilege separation: it may
// only do what its role allows on its path.
type ScopedToken struct {
	Name      string `json:"name"`
	ID        string `json:"tokenId" jsonschema:"proxbase@pve!name"`
	Secret    string `json:"tokenSecret,omitempty" jsonschema:"only returned when the token is created"`
	Value     string `json:"apiToken,omitempty" jsonschema:"the value for Authorization: PVEAPIToken=<apiToken> headers (tokenId=tokenSecret)"`
	Role      string `json:"role,omitempty"`
	Path      string `json:"path,omitempty"`
	Propagate bool   `json:"propagate" jsonschema:"the role also applies below path"`
}

// CheckTokenName validates the name of a scoped token.
func CheckTokenName(name string) error {
	if !tokenNameRe.MatchString(name) {
		return fmt.Errorf("invalid token name %q: lowercase letters, digits and dashes, starting with a letter, at most 20", name)
	}
	if name == mainToken {
		return fmt.Errorf("%q is the cluster's main token; choose another name", name)
	}
	return nil
}

func tokenPath(name string) string {
	return "/access/users/" + url.PathEscape(TokenUser) + "/token/" + url.PathEscape(name)
}

func tokenSecretFile(name string) string { return "token-" + name }

// CreateToken creates the token TokenUser!name, replacing one of that name, and
// grants it role on path. Its value is stored with the cluster's secrets.
func (c *Cluster) CreateToken(ctx context.Context, name, role, path string, propagate bool) (*ScopedToken, error) {
	if err := CheckTokenName(name); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("path %q must start with /, e.g. / or /vms/100", path)
	}
	api, err := c.TokenAPI("")
	if err != nil {
		return nil, err
	}
	var roles []struct {
		ID string `json:"roleid"`
	}
	if err := api.Get(ctx, "/access/roles", &roles); err != nil {
		return nil, err
	}
	ids := make([]string, len(roles))
	for i, r := range roles {
		ids[i] = r.ID
	}
	if !slices.Contains(ids, role) {
		slices.Sort(ids)
		return nil, fmt.Errorf("role %q does not exist; the cluster has %s", role, strings.Join(ids, ", "))
	}
	if err := api.Delete(ctx, tokenPath(name)); err != nil && !pve.IsStatus(err, 500) && !pve.IsStatus(err, 404) {
		return nil, fmt.Errorf("replace token %s: %w", name, err)
	}
	var tok struct {
		FullID string `json:"full-tokenid"`
		Value  string `json:"value"`
	}
	form := url.Values{"privsep": {"1"}, "comment": {fmt.Sprintf("proxbase: %s on %s", role, path)}}
	if err := api.Post(ctx, tokenPath(name), form, &tok); err != nil {
		return nil, fmt.Errorf("create token %s: %w", name, err)
	}
	acl := url.Values{"path": {path}, "roles": {role}, "tokens": {tok.FullID}, "propagate": {map[bool]string{true: "1", false: "0"}[propagate]}}
	if err := api.Put(ctx, "/access/acl", acl, nil); err != nil {
		_ = api.Delete(ctx, tokenPath(name))
		return nil, fmt.Errorf("grant %s on %s: %w", role, path, err)
	}
	t := &ScopedToken{Name: name, ID: tok.FullID, Secret: tok.Value, Value: tok.FullID + "=" + tok.Value, Role: role, Path: path, Propagate: propagate}
	if err := c.Dir.WriteSecret(tokenSecretFile(name), []byte(t.Value+"\n")); err != nil {
		return nil, err
	}
	return t, nil
}

// Tokens lists the scoped tokens of the cluster with their grants, without secrets.
func (c *Cluster) Tokens(ctx context.Context) ([]ScopedToken, error) {
	api, err := c.TokenAPI("")
	if err != nil {
		return nil, err
	}
	var tokens []struct {
		Name string `json:"tokenid"`
	}
	if err := api.Get(ctx, "/access/users/"+url.PathEscape(TokenUser)+"/token", &tokens); err != nil {
		return nil, err
	}
	var acls []struct {
		Path      string `json:"path"`
		Role      string `json:"roleid"`
		Type      string `json:"type"`
		ID        string `json:"ugid"`
		Propagate int    `json:"propagate"`
	}
	if err := api.Get(ctx, "/access/acl", &acls); err != nil {
		return nil, err
	}
	out := []ScopedToken{}
	for _, t := range tokens {
		if t.Name == mainToken {
			continue
		}
		st := ScopedToken{Name: t.Name, ID: TokenUser + "!" + t.Name}
		for _, a := range acls {
			if a.Type == "token" && a.ID == st.ID {
				st.Role, st.Path, st.Propagate = a.Role, a.Path, a.Propagate == 1
				break
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// DeleteToken deletes a scoped token; the API drops its grants with it.
func (c *Cluster) DeleteToken(ctx context.Context, name string) error {
	if err := CheckTokenName(name); err != nil {
		return err
	}
	api, err := c.TokenAPI("")
	if err != nil {
		return err
	}
	if err := api.Delete(ctx, tokenPath(name)); err != nil {
		return fmt.Errorf("delete token %s: %w", name, err)
	}
	if err := os.Remove(c.Dir.Secret(tokenSecretFile(name))); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// EnvWithToken is Env with the scoped token name instead of the main token.
func (c *Cluster) EnvWithToken(name string) (*Env, error) {
	if err := CheckTokenName(name); err != nil {
		return nil, err
	}
	e, err := c.Env()
	if err != nil {
		return nil, err
	}
	v, err := c.Dir.ReadSecret(tokenSecretFile(name))
	if err != nil {
		return nil, fmt.Errorf("cluster %s has no token %q created by proxbase (proxbase token create)", c.Cfg.Name, name)
	}
	e.APIToken = strings.TrimSpace(v)
	e.TokenID, e.TokenSecret, _ = strings.Cut(e.APIToken, "=")
	return e, nil
}
