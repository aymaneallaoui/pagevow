package version_test

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aymaneallaoui/pagevow/internal/version"
)

func TestGetFillsEveryField(t *testing.T) {
	info := version.Get()
	assert.NotEmpty(t, info.Version)
	assert.NotEmpty(t, info.Commit)
	assert.NotEmpty(t, info.Date)
	assert.Equal(t, runtime.Version(), info.Go)
	assert.Equal(t, runtime.GOOS, info.OS)
	assert.Equal(t, runtime.GOARCH, info.Arch)
}

func TestStringMentionsVersionAndCommit(t *testing.T) {
	info := version.Info{Version: "1.2.3", Commit: "abc123", Date: "2026-09-29", Go: "go1.27", OS: "linux", Arch: "amd64"}
	assert.Equal(t, "pagevow 1.2.3 (commit abc123, built 2026-09-29, go1.27 linux/amd64)", info.String())
}
