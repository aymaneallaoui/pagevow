package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/aymaneallaoui/pagevow/internal/testsfile"
)

const (
	maxDirBytes   = 120
	hashBytes     = 4
	reportFile    = "report.json"
	retrySuffix   = ".retry"
	clashSeparate = "-"
)

// planDirectories names the directory of every attempt of every test before anything runs. Names are unique when
// compared without case and without trailing dots or spaces, so a clash never makes one attempt overwrite another.
func planDirectories(tests []testsfile.Test, retries int) [][]string {
	taken := map[string]bool{directoryKey(reportFile): true}
	plans := make([][]string, len(tests))
	for i, test := range tests {
		base := baseDirectory(test.ID)
		for n := 1; ; n++ {
			candidate := base
			if n > 1 {
				candidate = withSuffix(base, clashSeparate+strconv.Itoa(n))
			}
			names := attemptNames(candidate, retries)
			if !anyTaken(taken, names) {
				for _, name := range names {
					taken[directoryKey(name)] = true
				}
				plans[i] = names
				break
			}
		}
	}
	return plans
}

func attemptNames(base string, retries int) []string {
	names := make([]string, 0, retries+1)
	names = append(names, base)
	for n := 1; n <= retries; n++ {
		names = append(names, base+retrySuffix+strconv.Itoa(n))
	}
	return names
}

func anyTaken(taken map[string]bool, names []string) bool {
	for _, name := range names {
		if taken[directoryKey(name)] {
			return true
		}
	}
	return false
}

// directoryKey is what a case-insensitive file system that drops trailing dots and spaces sees as the name.
func directoryKey(name string) string {
	return strings.ToLower(strings.TrimRight(name, ". "))
}

// baseDirectory derives a directory name from a test id that is safe on every OS: characters outside letters, digits,
// dot, underscore and hyphen become underscores, and long or device-like names are shortened or prefixed.
func baseDirectory(id string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '_'
	}, id)
	if trimmed := strings.TrimRight(name, "."); len(trimmed) < len(name) {
		name = trimmed + strings.Repeat("_", len(name)-len(trimmed))
	}
	if name == "" || reservedDeviceName(name) {
		name = "_" + name
	}
	if len(name) > maxDirBytes {
		digest := sha256.Sum256([]byte(id))
		suffix := clashSeparate + hex.EncodeToString(digest[:hashBytes])
		name = name[:maxDirBytes-len(suffix)] + suffix
	}
	return name
}

func withSuffix(base, suffix string) string {
	if len(base)+len(suffix) > maxDirBytes {
		base = base[:maxDirBytes-len(suffix)]
	}
	return base + suffix
}

// reservedDeviceName reports whether Windows treats the name as a device, with or without an extension.
func reservedDeviceName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) {
		return stem[3] >= '1' && stem[3] <= '9'
	}
	return false
}
