package selfupdate

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a release version vMAJOR.MINOR.PATCH[-PRERELEASE] (semver), optionally
// with the suffix `git describe` adds to builds after a tag: -N-gHASH and -dirty.
type Version struct {
	Major, Minor, Patch int
	Pre                 string // pre-release, e.g. rc.1
	Commits             int    // commits after the tag
	Dirty               bool   // built from a modified tree
}

var (
	semverRe   = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
	describeRe = regexp.MustCompile(`-([0-9]+)-g[0-9a-f]{4,}$`)
)

// ParseVersion parses v0.2.2, 0.2.2 (GoReleaser's .Version), v0.3.0-rc.1, the
// output of `git describe --tags --dirty` such as v0.2.2-7-gabc1234-dirty, and Go
// pseudo-versions. Build metadata (+...) is ignored except for +dirty.
func ParseVersion(s string) (Version, error) {
	var v Version
	t, meta, _ := strings.Cut(strings.TrimPrefix(s, "v"), "+")
	v.Dirty = strings.Contains(meta, "dirty")
	if rest, ok := strings.CutSuffix(t, "-dirty"); ok {
		t, v.Dirty = rest, true
	}
	if m := describeRe.FindStringSubmatchIndex(t); m != nil {
		n, err := strconv.Atoi(t[m[2]:m[3]])
		if err != nil {
			return Version{}, fmt.Errorf("invalid version %q", s)
		}
		t, v.Commits = t[:m[0]], n
	}
	m := semverRe.FindStringSubmatch(t)
	if m == nil {
		return Version{}, fmt.Errorf("invalid version %q", s)
	}
	for i, p := range []*int{&v.Major, &v.Minor, &v.Patch} {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, fmt.Errorf("invalid version %q", s)
		}
		*p = n
	}
	v.Pre = m[4]
	return v, nil
}

// IsRelease reports whether v names a release tag (no commits after it, clean tree).
func (v Version) IsRelease() bool { return v.Commits == 0 && !v.Dirty }

// Tag is the release tag of v, e.g. v0.2.2 or v0.3.0-rc.1, without a describe suffix.
func (v Version) Tag() string {
	tag := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		tag += "-" + v.Pre
	}
	return tag
}

// Compare returns -1, 0 or +1 when v is older than, the same as or newer than w.
// Pre-releases sort before their release (semver); a build after a tag, with
// commits or a dirty tree, sorts after the tag.
func (v Version) Compare(w Version) int {
	return cmp.Or(
		cmp.Compare(v.Major, w.Major),
		cmp.Compare(v.Minor, w.Minor),
		cmp.Compare(v.Patch, w.Patch),
		comparePre(v.Pre, w.Pre),
		cmp.Compare(v.Commits, w.Commits),
		cmp.Compare(btoi(v.Dirty), btoi(w.Dirty)),
	)
}

// comparePre orders pre-releases by semver §11: none sorts last, numeric
// identifiers numerically and before alphanumeric ones, a longer list after its prefix.
func comparePre(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(as), len(bs)) {
		x, xerr := strconv.ParseUint(as[i], 10, 64)
		y, yerr := strconv.ParseUint(bs[i], 10, 64)
		var c int
		switch {
		case xerr == nil && yerr == nil:
			c = cmp.Compare(x, y)
		case xerr == nil:
			c = -1
		case yerr == nil:
			c = 1
		default:
			c = strings.Compare(as[i], bs[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(as), len(bs))
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Change is what installing a release over the running version means.
type Change int

const (
	Unknown   Change = iota // the running version is not comparable, e.g. "dev"
	Same                    // the running version is the release
	Upgrade                 // the release is newer
	Downgrade               // the release is older, or the running build is newer
)

// Classify compares the running version current with the release tag target.
func Classify(current, target string) Change {
	cur, err := ParseVersion(current)
	if err != nil {
		return Unknown
	}
	tgt, err := ParseVersion(target)
	if err != nil {
		return Unknown
	}
	switch cur.Compare(tgt) {
	case -1:
		return Upgrade
	case 1:
		return Downgrade
	}
	return Same
}
