package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/doctor"
	"github.com/virtbase/proxbase/internal/golden"
)

type imagesOut struct {
	Images []imageEntry `json:"images"`
}

type imageEntry struct {
	Key     string   `json:"key"`
	ISO     string   `json:"iso"`
	Root    string   `json:"root" jsonschema:"root file system and size, e.g. zfs 32G"`
	Ceph    string   `json:"ceph,omitempty" jsonschema:"Ceph release installed in the image"`
	Created string   `json:"created" jsonschema:"RFC 3339"`
	UsedBy  []string `json:"usedBy" jsonschema:"clusters whose disks are overlays of the image"`
}

type pruneIn struct {
	DryRun bool `json:"dryRun,omitempty" jsonschema:"only report what would be deleted"`
}

type pruneOut struct {
	Deleted []string `json:"deleted" jsonschema:"keys of deleted (or, with dryRun, unused) images"`
	Kept    []string `json:"kept,omitempty" jsonschema:"images in use, with their clusters"`
}

type hostCheckIn struct {
	PortBase int `json:"portBase,omitempty" jsonschema:"access.portBase whose first ports to check (default 18000)"`
}

type hostCheckOut struct {
	OK     bool           `json:"ok" jsonschema:"no check failed; warnings do not block clusters"`
	Checks []doctor.Check `json:"checks"`
}

func (h *handlers) imageList(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, imagesOut, error) {
	images, err := golden.List()
	if err != nil {
		return nil, imagesOut{}, err
	}
	users, err := golden.Users()
	if err != nil {
		return nil, imagesOut{}, err
	}
	out := imagesOut{Images: []imageEntry{}}
	for _, m := range images {
		out.Images = append(out.Images, imageEntry{
			Key: m.Key, ISO: m.ISO, Root: m.Filesystem + " " + m.Size, Ceph: m.Ceph,
			Created: m.Created.UTC().Format(time.RFC3339), UsedBy: nonNil(users[m.Key]),
		})
	}
	return nil, out, nil
}

func (h *handlers) imagePrune(_ context.Context, _ *mcp.CallToolRequest, in pruneIn) (*mcp.CallToolResult, pruneOut, error) {
	images, err := golden.List()
	if err != nil {
		return nil, pruneOut{}, err
	}
	users, err := golden.Users()
	if err != nil {
		return nil, pruneOut{}, err
	}
	out := pruneOut{Deleted: []string{}}
	for _, m := range images {
		if u := users[m.Key]; len(u) > 0 {
			out.Kept = append(out.Kept, fmt.Sprintf("%s (used by %s)", m.Key, strings.Join(u, ", ")))
			continue
		}
		if !in.DryRun {
			if err := golden.Remove(m.Key); err != nil {
				return nil, out, err
			}
		}
		out.Deleted = append(out.Deleted, m.Key)
	}
	return nil, out, nil
}

func (h *handlers) hostCheck(_ context.Context, _ *mcp.CallToolRequest, in hostCheckIn) (*mcp.CallToolResult, hostCheckOut, error) {
	base := in.PortBase
	if base <= 0 {
		base = 18000
	}
	checks := doctor.Run(base)
	return nil, hostCheckOut{OK: !doctor.Failed(checks), Checks: checks}, nil
}
