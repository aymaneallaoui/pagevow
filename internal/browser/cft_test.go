package browser

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPinTableCoversEverySupportedPlatform(t *testing.T) {
	table := pinTable()
	require.Len(t, table, 6)
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	seen := map[string]bool{}
	for _, pin := range table {
		assert.False(t, seen[pin.Platform], pin.Platform)
		seen[pin.Platform] = true
		assert.Positive(t, pin.Size, pin.Platform)
		assert.Regexp(t, hex64, pin.SHA256, pin.Platform)
		assert.NotEmpty(t, pin.Executable, pin.Platform)
		assert.Equal(t, "chrome-"+pin.Platform+".zip", pin.ArchiveName())
		assert.Contains(t, pin.Executable, "chrome-"+pin.Platform+"/", pin.Platform)
	}
}

func TestPinURL(t *testing.T) {
	pin, err := PinFor("linux", "amd64")
	require.NoError(t, err)
	assert.Equal(t,
		"https://storage.googleapis.com/chrome-for-testing-public/154.0.8037.92/linux64/chrome-linux64.zip",
		pin.URL(DefaultBaseURL))
}

func TestPlatformName(t *testing.T) {
	tests := []struct {
		goos, goarch string
		want         string
		wantErr      string
	}{
		{"linux", "amd64", "linux64", ""},
		{"linux", "arm64", "linux-arm64", ""},
		{"darwin", "arm64", "mac-arm64", ""},
		{"darwin", "amd64", "mac-x64", ""},
		{"windows", "amd64", "win64", ""},
		{"windows", "386", "win32", ""},
		{"windows", "arm64", "", "windows/arm64"},
		{"freebsd", "amd64", "", "freebsd/amd64"},
		{"linux", "riscv64", "", "linux/riscv64"},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			got, err := PlatformName(tt.goos, tt.goarch)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Contains(t, err.Error(), "no Chrome for Testing build")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			pin, err := PinFor(tt.goos, tt.goarch)
			require.NoError(t, err)
			assert.Equal(t, tt.want, pin.Platform)
		})
	}
}

func TestPinForMacExecutableIsInsideTheAppBundle(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		pin, err := PinFor("darwin", arch)
		require.NoError(t, err)
		assert.Contains(t, pin.Executable, "Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing")
	}
}
