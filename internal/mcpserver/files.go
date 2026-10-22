package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/cluster"
)

const (
	filePutDescription = `Write a file on a node as root, e.g. a config, script or test fixture: text in content, or binary data in
contentBase64 (at most 4 MiB). The file is replaced atomically; missing directories are created. Files in /etc/pve
(the cluster file system, shared by all nodes) get its fixed permissions.`
	fileGetDescription = `Read a file of a node: text as content, other data as contentBase64; at most maxBytes (default 256 KiB,
max 4 MiB), truncated=true if the file is longer.`
)

type filePutIn struct {
	Cluster       string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Node          string `json:"node,omitempty" jsonschema:"node name, e.g. pve1 (default: the first node)"`
	Path          string `json:"path" jsonschema:"absolute path on the node"`
	Content       string `json:"content,omitempty" jsonschema:"file content as text"`
	ContentBase64 string `json:"contentBase64,omitempty" jsonschema:"file content, base64-encoded; instead of content"`
	Mode          string `json:"mode,omitempty" jsonschema:"permissions in octal, e.g. 0755 (default 0644)"`
}

type filePutOut struct {
	Node   string `json:"node"`
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type fileGetIn struct {
	Cluster  string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Node     string `json:"node,omitempty" jsonschema:"node name, e.g. pve1 (default: the first node)"`
	Path     string `json:"path" jsonschema:"absolute path on the node"`
	MaxBytes int    `json:"maxBytes,omitempty" jsonschema:"read at most this many bytes (default 256 KiB, max 4 MiB)"`
}

type fileGetOut struct {
	Node          string `json:"node"`
	Path          string `json:"path"`
	Size          int64  `json:"size" jsonschema:"size of the whole file"`
	Truncated     bool   `json:"truncated,omitempty" jsonschema:"only the first maxBytes were read"`
	Content       string `json:"content,omitempty" jsonschema:"the data, if it is UTF-8 text"`
	ContentBase64 string `json:"contentBase64,omitempty" jsonschema:"the data, base64-encoded, if it is not text"`
	SHA256        string `json:"sha256" jsonschema:"of the returned data"`
}

const (
	// maxFileBytes bounds the files the tools copy: results travel as JSON.
	maxFileBytes     = 4 << 20
	defaultFileBytes = 256 << 10
	// fileTimeout bounds a copy.
	fileTimeout = time.Minute
)

func (h *handlers) filePut(ctx context.Context, req *mcp.CallToolRequest, in filePutIn) (*mcp.CallToolResult, filePutOut, error) {
	data := []byte(in.Content)
	if in.ContentBase64 != "" {
		if in.Content != "" {
			return nil, filePutOut{}, fmt.Errorf("set content or contentBase64, not both")
		}
		var err error
		if data, err = base64.StdEncoding.DecodeString(in.ContentBase64); err != nil {
			return nil, filePutOut{}, fmt.Errorf("contentBase64: %w", err)
		}
	}
	if len(data) > maxFileBytes {
		return nil, filePutOut{}, fmt.Errorf("file has %d bytes, more than %d; copy large files with proxbase cp", len(data), maxFileBytes)
	}
	mode := os.FileMode(0o644)
	if in.Mode != "" {
		var err error
		if mode, err = cluster.ParseFileMode(in.Mode); err != nil {
			return nil, filePutOut{}, err
		}
	}
	c, err := h.open(in.Cluster, nil)
	if err != nil {
		return nil, filePutOut{}, err
	}
	node := in.Node
	if node == "" {
		node = c.St.Nodes[0].Name
	}
	ctx, cancel := context.WithTimeout(ctx, within(h.limit(req), fileTimeout))
	defer cancel()
	if err := c.PutFile(ctx, node, in.Path, bytes.NewReader(data), mode); err != nil {
		return nil, filePutOut{}, err
	}
	return nil, filePutOut{Node: node, Path: in.Path, Bytes: len(data), SHA256: sha256Hex(data)}, nil
}

func (h *handlers) fileGet(ctx context.Context, req *mcp.CallToolRequest, in fileGetIn) (*mcp.CallToolResult, fileGetOut, error) {
	c, err := h.open(in.Cluster, nil)
	if err != nil {
		return nil, fileGetOut{}, err
	}
	node := in.Node
	if node == "" {
		node = c.St.Nodes[0].Name
	}
	limit := int64(defaultFileBytes)
	if in.MaxBytes > 0 {
		limit = int64(min(in.MaxBytes, maxFileBytes))
	}
	ctx, cancel := context.WithTimeout(ctx, within(h.limit(req), fileTimeout))
	defer cancel()
	var buf bytes.Buffer
	size, err := c.GetFile(ctx, node, in.Path, &buf, limit)
	if err != nil {
		return nil, fileGetOut{}, err
	}
	data := buf.Bytes()
	out := fileGetOut{Node: node, Path: in.Path, Size: size, Truncated: size > int64(len(data)), SHA256: sha256Hex(data)}
	if utf8.Valid(data) {
		out.Content = string(data)
	} else {
		out.ContentBase64 = base64.StdEncoding.EncodeToString(data)
	}
	return nil, out, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
