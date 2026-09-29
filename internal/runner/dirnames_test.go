package runner

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/testsfile"
)

func testsWithIDs(ids ...string) []testsfile.Test {
	tests := make([]testsfile.Test, len(ids))
	for i, id := range ids {
		tests[i] = testsfile.Test{ID: id}
	}
	return tests
}

func requireNoClash(t *testing.T, plans [][]string) {
	t.Helper()
	seen := map[string]string{directoryKey(reportFile): reportFile}
	for _, names := range plans {
		for _, name := range names {
			key := directoryKey(name)
			previous, clash := seen[key]
			require.False(t, clash, "%q clashes with %q", name, previous)
			seen[key] = name
			assert.LessOrEqual(t, len(name), maxDirBytes+len(retrySuffix)+3, name)
			assert.Equal(t, name, strings.TrimRight(name, ". "), "trailing dot or space")
		}
	}
}

func TestPlannedDirectoriesReserveEveryRetryName(t *testing.T) {
	plans := planDirectories(testsWithIDs("home", "home.retry1"), 1)

	assert.Equal(t, []string{"home", "home.retry1"}, plans[0])
	assert.Equal(t, []string{"home.retry1-2", "home.retry1-2.retry1"}, plans[1])
	requireNoClash(t, plans)
}

func TestPlannedDirectoriesClashInEitherOrder(t *testing.T) {
	plans := planDirectories(testsWithIDs("home.retry1", "home"), 1)

	assert.Equal(t, []string{"home.retry1", "home.retry1.retry1"}, plans[0])
	assert.Equal(t, []string{"home-2", "home-2.retry1"}, plans[1])
	requireNoClash(t, plans)
}

func TestPlannedDirectoriesWithoutRetriesOnlyReserveTheBaseName(t *testing.T) {
	plans := planDirectories(testsWithIDs("home", "home.retry1"), 0)

	assert.Equal(t, [][]string{{"home"}, {"home.retry1"}}, plans)
}

func TestPlannedDirectoriesIgnoreCase(t *testing.T) {
	plans := planDirectories(testsWithIDs("Login", "login", "LOGIN", "LOGIN-2"), 1)

	assert.Equal(t, "Login", plans[0][0])
	assert.Equal(t, "login-2", plans[1][0])
	assert.Equal(t, "LOGIN-3", plans[2][0])
	assert.Equal(t, "LOGIN-2-2", plans[3][0])
	requireNoClash(t, plans)
}

func TestPlannedDirectoriesIgnoreTrailingDotsAndSpaces(t *testing.T) {
	plans := planDirectories(testsWithIDs("a.", "a", "a..", "a_", "b ", "b"), 1)

	requireNoClash(t, plans)
	for _, names := range plans {
		for _, name := range names {
			assert.NotEqual(t, "", name)
		}
	}
	assert.Equal(t, "a_", plans[0][0])
	assert.Equal(t, "a", plans[1][0])
}

func TestKeyComparesWithoutCaseTrailingDotsAndSpaces(t *testing.T) {
	assert.Equal(t, directoryKey("Login"), directoryKey("login"))
	assert.Equal(t, directoryKey("a."), directoryKey("a"))
	assert.Equal(t, directoryKey("a. ."), directoryKey("A"))
	assert.NotEqual(t, directoryKey("a.b"), directoryKey("a"))
}

func TestReportFileNameIsReserved(t *testing.T) {
	plans := planDirectories(testsWithIDs("report.json", "REPORT.JSON"), 0)

	assert.Equal(t, "report.json-2", plans[0][0])
	assert.Equal(t, "REPORT.JSON-3", plans[1][0])
	requireNoClash(t, plans)
}

func TestLongIDsAreCutWithAHashAndStayDistinct(t *testing.T) {
	long := strings.Repeat("x", 300)
	plans := planDirectories(testsWithIDs(long+"a", long+"b", strings.Repeat("y", 120), strings.Repeat("y", 121)), 2)

	requireNoClash(t, plans)
	assert.Len(t, plans[0][0], maxDirBytes)
	assert.Len(t, plans[1][0], maxDirBytes)
	assert.NotEqual(t, plans[0][0], plans[1][0])
	assert.Equal(t, strings.Repeat("y", 120), plans[2][0])
	assert.Len(t, plans[3][0], maxDirBytes)
	assert.NotEqual(t, plans[2][0], plans[3][0])
	assert.Equal(t, plans[0][0]+".retry1", plans[0][1])
}

func TestClashSuffixNeverPushesANameOverTheLimit(t *testing.T) {
	long := strings.Repeat("z", 200)
	tests := testsWithIDs(long, long+" ")
	plans := planDirectories(tests, 1)

	requireNoClash(t, plans)
	for _, names := range plans {
		assert.LessOrEqual(t, len(names[0]), maxDirBytes)
	}
}

func TestWindowsDeviceNamesGetAPrefix(t *testing.T) {
	ids := []string{"CON", "con", "PRN", "AUX", "NUL", "COM1", "com9", "LPT1", "lpt9", "nul.txt", "CON.retry1", "Aux.tar.gz"}
	plans := planDirectories(testsWithIDs(ids...), 1)

	requireNoClash(t, plans)
	for i, names := range plans {
		assert.True(t, strings.HasPrefix(names[0], "_"), "%s -> %s", ids[i], names[0])
		assert.False(t, reservedDeviceName(names[0]), names[0])
		assert.False(t, reservedDeviceName(names[1]), names[1])
	}
	for _, id := range []string{"COM0", "COM10", "CONSOLE", "LPT", "com", "nulled", "x.con", "auxiliary"} {
		assert.Equal(t, id, baseDirectory(id), id)
	}
}

func TestUnsafeAndEmptyIDsGetSafeNames(t *testing.T) {
	assert.Equal(t, ".._evil", baseDirectory("../evil")[:7])
	assert.Equal(t, "_", baseDirectory(""))
	assert.Equal(t, "____", baseDirectory("...."))
	assert.Equal(t, "a_b_c", baseDirectory("a b/c"))
	assert.Equal(t, "caf_", baseDirectory("café"))
	for _, id := range []string{"a/b", `a\b`, "a:b", "a*b", "a?b", "a|b", "a\x00b"} {
		assert.NotContains(t, baseDirectory(id), string(id[1]), id)
	}
}
