package ctl

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a parsed Semantic Version (https://semver.org). The leading "v" of a git tag is accepted.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
	Raw                 string
}

// semverRe is the pattern published on semver.org (with an optional leading "v"): numeric parts
// without leading zeros, pre-release identifiers that are never empty, no "..".
var semverRe = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// ParseVersion parses a version string. ok is false for non-SemVer builds such as "dev" or a
// "0.1.0-3-gabc1234" git-describe string (which is SemVer-shaped and parses as a pre-release).
func ParseVersion(s string) (Version, bool) {
	m := semverRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{Raw: s}, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	return Version{Major: a, Minor: b, Patch: c, Pre: m[4], Raw: s}, true
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare returns -1, 0 or 1 following SemVer precedence (a pre-release is lower than its release).
func (v Version) Compare(o Version) int {
	for _, p := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case v.Pre == o.Pre:
		return 0
	case v.Pre == "":
		return 1
	case o.Pre == "":
		return -1
	}
	return comparePre(v.Pre, o.Pre)
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := as[i], bs[i]
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		switch {
		case xerr == nil && yerr == nil:
			if xn != yn {
				if xn < yn {
					return -1
				}
				return 1
			}
		case xerr == nil:
			return -1
		case yerr == nil:
			return 1
		case x != y:
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}

// Compatibility policy (documented in docs/cli.md):
//
//   - The management command and the server are released together and carry the same version, but they
//     are separate programs, so they do not have to be identical.
//   - A "compatibility line" is MAJOR for versions >= 1.0.0 and MAJOR.MINOR for 0.y.z (before 1.0 a
//     minor release may change interfaces). The command manages servers of its own line.
//   - Moving the server to another line is a breaking change and needs explicit confirmation.

// SameLine reports whether two versions are on the same compatibility line.
func SameLine(a, b Version) bool {
	if a.Major != b.Major {
		return false
	}
	if a.Major == 0 {
		return a.Minor == b.Minor
	}
	return true
}

// CompatNote describes the relationship between the CLI and a server version for humans.
// level is "ok", "warn" or "unknown".
func CompatNote(cli, server string) (level, note string) {
	c, cok := ParseVersion(cli)
	s, sok := ParseVersion(server)
	if !cok || !sok {
		return "unknown", fmt.Sprintf("cannot compare %q and %q (not release versions); this is normal for development builds", cli, server)
	}
	if SameLine(c, s) {
		return "ok", fmt.Sprintf("command %s and server %s are on the same compatibility line", c, s)
	}
	return "warn", fmt.Sprintf("command %s and server %s are on different compatibility lines; install the matching command with install.sh --version %s", c, s, s)
}
