package update

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)` +
	`(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

var describePattern = regexp.MustCompile(`(?:^|[.-])(?:[0-9]+-g[0-9a-f]{7,40}(?:-dirty)?|dirty)$`)

type semver struct {
	core [3]uint64
	pre  []string
}

func parseVersion(text string) (semver, bool) {
	match := versionPattern.FindStringSubmatch(strings.TrimPrefix(text, "v"))
	if match == nil || describePattern.MatchString(match[4]) {
		return semver{}, false
	}
	var v semver
	for i := range v.core {
		n, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return semver{}, false
		}
		v.core[i] = n
	}
	if match[4] != "" {
		v.pre = strings.Split(match[4], ".")
	}
	return v, true
}

func (a semver) compare(b semver) int {
	for i := range a.core {
		if c := cmp.Compare(a.core[i], b.core[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := range min(len(a.pre), len(b.pre)) {
		if c := comparePreRelease(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a.pre), len(b.pre))
}

func comparePreRelease(a, b string) int {
	aNum, bNum := isDigits(a), isDigits(b)
	switch {
	case aNum && bNum:
		a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
		if c := cmp.Compare(len(a), len(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return strings.Compare(a, b)
}

func isDigits(text string) bool {
	return text != "" && strings.Trim(text, "0123456789") == ""
}

// Comparable reports whether a version is MAJOR.MINOR.PATCH with an optional leading v, pre-release and build part, and not a git describe build.
func Comparable(version string) bool {
	_, ok := parseVersion(version)
	return ok
}

// NewerThan reports whether latest is a later release than current, counting a current version that is not a release as older.
func NewerThan(latest, current string) (bool, error) {
	l, ok := parseVersion(latest)
	if !ok {
		return false, fmt.Errorf("release version %q is not MAJOR.MINOR.PATCH", latest)
	}
	c, ok := parseVersion(current)
	if !ok {
		return true, nil
	}
	return l.compare(c) > 0, nil
}
