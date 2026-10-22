package mcpserver

import (
	"cmp"
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/cluster"
)

const tokenCreateDescription = `Create an API token that may only do what a role allows on a path, to test code with least privilege
or check that an operation is refused: e.g. role PVEAuditor on /, PVEVMAdmin on /vms/100, PVEDatastoreUser on
/storage/tank. Replaces a token of the same name. cluster_env with token returns the endpoint and CA with it.`

type tokenCreateIn struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Name      string `json:"name" jsonschema:"token name: lowercase letters, digits and dashes; the token is proxbase@pve!name"`
	Role      string `json:"role" jsonschema:"a role of the cluster, e.g. PVEAuditor, PVEVMUser, PVEVMAdmin, PVEDatastoreUser, PVEPoolAdmin"`
	Path      string `json:"path,omitempty" jsonschema:"ACL path the role applies to (default /), e.g. /vms/100, /storage/tank, /pool/ci"`
	Propagate *bool  `json:"propagate,omitempty" jsonschema:"the role also applies below path (default true)"`
}

type tokenNameIn struct {
	Cluster string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Name    string `json:"name" jsonschema:"token name"`
}

type tokensOut struct {
	Tokens []cluster.ScopedToken `json:"tokens"`
}

// tokenTimeout bounds the API calls of a token tool.
const tokenTimeout = 30 * time.Second

func (h *handlers) tokenCreate(ctx context.Context, req *mcp.CallToolRequest, in tokenCreateIn) (*mcp.CallToolResult, *cluster.ScopedToken, error) {
	c, err := h.open(in.Cluster, nil)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, within(h.limit(req), tokenTimeout))
	defer cancel()
	t, err := c.CreateToken(ctx, in.Name, in.Role, cmp.Or(in.Path, "/"), in.Propagate == nil || *in.Propagate)
	return nil, t, err
}

func (h *handlers) tokenList(ctx context.Context, req *mcp.CallToolRequest, in clusterIn) (*mcp.CallToolResult, tokensOut, error) {
	c, err := h.open(in.Cluster, nil)
	if err != nil {
		return nil, tokensOut{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, within(h.limit(req), tokenTimeout))
	defer cancel()
	tokens, err := c.Tokens(ctx)
	return nil, tokensOut{Tokens: tokens}, err
}

func (h *handlers) tokenDelete(ctx context.Context, req *mcp.CallToolRequest, in tokenNameIn) (*mcp.CallToolResult, messageOut, error) {
	c, err := h.open(in.Cluster, nil)
	if err != nil {
		return nil, messageOut{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, within(h.limit(req), tokenTimeout))
	defer cancel()
	if err := c.DeleteToken(ctx, in.Name); err != nil {
		return nil, messageOut{}, err
	}
	return nil, messageOut{Message: "token " + cluster.TokenUser + "!" + in.Name + " deleted"}, nil
}
