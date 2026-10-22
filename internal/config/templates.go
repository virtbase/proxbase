package config

import (
	"encoding/hex"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Templates are guests every create (and node add) provides, as fixtures for
// tests: VM templates from cloud images and container templates.
type Templates struct {
	VMs        []VMTemplate `yaml:"vms,omitempty" json:"vms,omitempty"`
	Containers []string     `yaml:"containers,omitempty" json:"containers,omitempty" jsonschema:"description=Container templates as named by pveam\\, e.g. debian-13-standard or ubuntu-24.04-standard (newest version); copied to every node"`
}

// VMTemplate is a VM template imported from a cloud image.
type VMTemplate struct {
	Name     string `yaml:"name" json:"name" jsonschema:"description=Name of the template in Proxmox VE"`
	Image    string `yaml:"image" json:"image" jsonschema:"description=A known image (debian-13\\, debian-12\\, ubuntu-24.04\\, ubuntu-22.04) or an https URL of a qcow2 or raw image"`
	Checksum string `yaml:"checksum,omitempty" json:"checksum,omitempty" jsonschema:"pattern=^(sha256|sha512):[0-9a-f]+$,description=sha256:<hex> or sha512:<hex> of an image given by URL (required for URLs)"`
	VMID     int    `yaml:"vmid" json:"vmid" jsonschema:"minimum=100,maximum=999999999"`
	Storage  string `yaml:"storage,omitempty" json:"storage,omitempty" jsonschema:"description=Storage for the disk (default: the root disk's storage\\, local-zfs on ZFS)"`
	Memory   int    `yaml:"memory,omitempty" json:"memory,omitempty" jsonschema:"description=MiB (default 1024)"`
	Cores    int    `yaml:"cores,omitempty" json:"cores,omitempty" jsonschema:"description=default 1"`
	DiskSize string `yaml:"diskSize,omitempty" json:"diskSize,omitempty" jsonschema:"pattern=^[0-9]+[MGT]$,description=Grow the disk to this size (default: the image's size)"`
	PerNode  bool   `yaml:"perNode,omitempty" json:"perNode,omitempty" jsonschema:"description=A copy on every node (VMID vmid+n-1 on node n)\\, for clones on local storage; default: on the first node only"`
}

// CloudImage is a cloud image known by name, with the checksum file that lists it.
type CloudImage struct {
	URL       string // the image
	SumsURL   string // SHA256SUMS or SHA512SUMS next to it
	Algorithm string // sha256 or sha512
}

// CloudImages are the images that templates can name instead of a URL.
var CloudImages = map[string]CloudImage{
	"debian-13":    {"https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2", "https://cloud.debian.org/images/cloud/trixie/latest/SHA512SUMS", "sha512"},
	"debian-12":    {"https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2", "https://cloud.debian.org/images/cloud/bookworm/latest/SHA512SUMS", "sha512"},
	"ubuntu-24.04": {"https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img", "https://cloud-images.ubuntu.com/noble/current/SHA256SUMS", "sha256"},
	"ubuntu-22.04": {"https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img", "https://cloud-images.ubuntu.com/jammy/current/SHA256SUMS", "sha256"},
}

var (
	guestNameRe     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	ctTemplateRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]*$`)
	checksumRe      = regexp.MustCompile(`^(sha256|sha512):[0-9a-f]+$`)
	checksumHexLens = map[string]int{"sha256": 64, "sha512": 128}
)

// ParseChecksum splits "sha256:<hex>" into the algorithm and the digest.
func ParseChecksum(s string) (algorithm string, digest []byte, err error) {
	if !checksumRe.MatchString(s) {
		return "", nil, fmt.Errorf("checksum %q must be sha256:<hex> or sha512:<hex>", s)
	}
	algorithm, h, _ := strings.Cut(s, ":")
	if len(h) != checksumHexLens[algorithm] {
		return "", nil, fmt.Errorf("checksum %q: a %s digest has %d hex digits", s, algorithm, checksumHexLens[algorithm])
	}
	digest, err = hex.DecodeString(h)
	return algorithm, digest, err
}

func (t *Templates) setDefaults() {
	for i := range t.VMs {
		v := &t.VMs[i]
		if v.Memory == 0 {
			v.Memory = 1024
		}
		if v.Cores == 0 {
			v.Cores = 1
		}
	}
}

// validateTemplates checks the templates of c.
func (c *Cluster) validateTemplates() []error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	vmids := map[int]string{}
	names := map[string]bool{}
	for i, v := range c.Templates.VMs {
		p := fmt.Sprintf("templates.vms[%d]: ", i)
		if !guestNameRe.MatchString(v.Name) || len(v.Name) > 63 {
			add("%sname %q must be a DNS name (lowercase letters, digits, dashes)", p, v.Name)
		}
		if names[v.Name] {
			add("%sname %q is used twice", p, v.Name)
		}
		names[v.Name] = true
		if _, known := CloudImages[v.Image]; !known {
			u, err := url.Parse(v.Image)
			switch {
			case err != nil || u.Scheme != "https" || u.Host == "":
				add("%simage %q must be one of %s or an https URL", p, v.Image, strings.Join(cloudImageNames(), ", "))
			case v.Checksum == "":
				add("%san image given by URL needs a checksum (sha256:<hex> or sha512:<hex>)", p)
			}
		}
		if v.Checksum != "" {
			if _, _, err := ParseChecksum(v.Checksum); err != nil {
				add("%s%v", p, err)
			}
		}
		if v.VMID < 100 || v.VMID > 999999999 {
			add("%svmid must be between 100 and 999999999", p)
		}
		last := v.VMID
		if v.PerNode {
			last = v.VMID + c.MaxNodeIndex() - 1
		}
		for id := v.VMID; id <= last; id++ {
			if other, ok := vmids[id]; ok {
				add("%svmid %d is also used by template %s", p, id, other)
				break
			}
			vmids[id] = v.Name
		}
		if v.Memory < 64 || v.Cores < 1 || v.Cores > 64 {
			add("%smemory must be at least 64 (MiB) and cores between 1 and 64", p)
		}
		if v.DiskSize != "" {
			if _, err := DiskMiB(v.DiskSize); err != nil {
				add("%sdiskSize: %v", p, err)
			}
		}
	}
	for i, ct := range c.Templates.Containers {
		if !ctTemplateRe.MatchString(ct) {
			add("templates.containers[%d]: %q must be a template name like debian-13-standard", i, ct)
		}
	}
	return errs
}

func cloudImageNames() []string { return slices.Sorted(maps.Keys(CloudImages)) }
