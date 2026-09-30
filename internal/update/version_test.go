package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewerThan(t *testing.T) {
	tests := []struct {
		name    string
		latest  string
		current string
		want    bool
	}{
		{"newer minor", "1.3.0", "1.2.0", true},
		{"newer patch", "1.2.4", "1.2.3", true},
		{"newer major", "2.0.0", "1.9.9", true},
		{"equal", "1.2.3", "1.2.3", false},
		{"older", "1.2.3", "1.3.0", false},
		{"numeric not lexical", "1.10.0", "1.9.0", true},
		{"v prefix on both", "v1.3.0", "v1.2.0", true},
		{"v prefix on one", "1.3.0", "v1.2.0", true},
		{"equal with different prefixes", "v1.2.3", "1.2.3", false},
		{"release beats its prerelease", "1.2.3", "1.2.3-rc.1", true},
		{"prerelease is not newer than its release", "1.2.3-rc.1", "1.2.3", false},
		{"later prerelease", "1.2.3-rc.2", "1.2.3-rc.1", true},
		{"numeric prerelease ids compare as numbers", "1.0.0-rc.10", "1.0.0-rc.9", true},
		{"alphanumeric beats numeric prerelease id", "1.0.0-alpha", "1.0.0-1", true},
		{"longer prerelease wins a tie", "1.0.0-alpha.1", "1.0.0-alpha", true},
		{"build metadata ignored", "1.2.3+b2", "1.2.3+b1", false},
		{"dev build counts as older", "0.0.1", "dev", true},
		{"empty current counts as older", "0.0.1", "", true},
		{"git describe current counts as older", "1.2.3", "v1.2.3-4-gabcdef1-dirty", true},
		{"git describe build of a newer tag counts as older", "1.2.3", "v1.2.4-5-gabc1234", true},
		{"dirty tag build counts as older", "1.2.3", "v1.2.3-dirty", true},
		{"pseudo version current", "0.1.0", "v0.0.0-20260930123456-abcdef123456", true},
		{"two part current counts as older", "1.0.0", "1.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewerThan(tt.latest, tt.current)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewerThanRejectsLatestThatIsNotAVersion(t *testing.T) {
	for _, latest := range []string{"", "latest", "1.2", "1.2.x", "v01.2.3", "1.2.3-", "99999999999999999999.0.0"} {
		_, err := NewerThan(latest, "1.0.0")
		assert.Error(t, err, latest)
	}
}

func TestComparable(t *testing.T) {
	assert.True(t, Comparable("1.2.3"))
	assert.True(t, Comparable("v1.2.3-rc.1+build.5"))
	assert.False(t, Comparable("dev"))
	assert.False(t, Comparable(""))
	assert.False(t, Comparable("1.2"))
}

func TestComparableRejectsBuildsFromGitDescribe(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"v1.2.3-5-gabc1234", false},
		{"1.2.3-5-gabc1234", false},
		{"v1.2.3-4-gabcdef1-dirty", false},
		{"v1.2.3-dirty", false},
		{"v1.2.3-rc.1-dirty", false},
		{"v1.2.3-rc.1-7-g0123456789ab", false},
		{"v1.2.3-rc.1", true},
		{"v1.2.3-rc.1+build.5", true},
		{"v1.2.3-5", true},
		{"v1.2.3-gabc1234", true},
		{"v1.2.3-5-gabc12", true},
		{"v1.2.3-dirtyish", true},
		{"v1.2.3+dirty", true},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			assert.Equal(t, tt.want, Comparable(tt.version))
		})
	}
}
