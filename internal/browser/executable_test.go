package browser

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeFinder(goos string, onPath map[string]string, files []string, env map[string]string) finder {
	present := map[string]bool{}
	for _, file := range files {
		present[file] = true
	}
	return finder{
		goos: goos,
		lookPath: func(name string) (string, error) {
			if path, ok := onPath[name]; ok {
				return path, nil
			}
			return "", errors.New("not found")
		},
		isFile: func(path string) bool { return present[path] },
		getenv: func(key string) string { return env[key] },
	}
}

func TestFindExecutableLinuxPrefersPath(t *testing.T) {
	f := fakeFinder("linux",
		map[string]string{"google-chrome": "/custom/google-chrome"},
		[]string{"/usr/bin/chromium"}, nil)
	got, err := f.find()
	require.NoError(t, err)
	assert.Equal(t, "/custom/google-chrome", got)
}

func TestFindExecutableLinuxPathOrder(t *testing.T) {
	f := fakeFinder("linux",
		map[string]string{"chromium": "/p/chromium", "google-chrome": "/p/google-chrome"}, nil, nil)
	got, err := f.find()
	require.NoError(t, err)
	assert.Equal(t, "/p/chromium", got)
}

func TestFindExecutableLinuxFallsBackToInstallLocations(t *testing.T) {
	f := fakeFinder("linux", nil, []string{"/opt/google/chrome/chrome"}, nil)
	got, err := f.find()
	require.NoError(t, err)
	assert.Equal(t, "/opt/google/chrome/chrome", got)
}

func TestFindExecutableDarwinChecksSystemAndUserApplications(t *testing.T) {
	system := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	user := filepath.Join("/Users/ada", "Applications", "Chromium.app/Contents/MacOS/Chromium")

	got, err := fakeFinder("darwin", nil, []string{system}, map[string]string{"HOME": "/Users/ada"}).find()
	require.NoError(t, err)
	assert.Equal(t, system, got)

	got, err = fakeFinder("darwin", nil, []string{system, user}, map[string]string{"HOME": "/Users/ada"}).find()
	require.NoError(t, err)
	assert.Equal(t, user, got, "Chromium is preferred over branded Chrome")
}

func TestFindExecutableDarwinIgnoresPathNames(t *testing.T) {
	f := fakeFinder("darwin", map[string]string{"chromium": "/usr/local/bin/chromium"}, nil, nil)
	_, err := f.find()
	assert.ErrorIs(t, err, ErrExecutableNotFound)
}

func TestFindExecutableWindowsUsesEnvironmentRoots(t *testing.T) {
	want := filepath.Join(`C:\Program Files (x86)`, "Google", "Chrome", "Application", "chrome.exe")
	f := fakeFinder("windows", nil, []string{want}, map[string]string{
		"ProgramFiles":      `C:\Program Files`,
		"ProgramFiles(x86)": `C:\Program Files (x86)`,
	})
	got, err := f.find()
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestFindExecutableWindowsSkipsUnsetRoots(t *testing.T) {
	f := fakeFinder("windows", nil, []string{filepath.Join("Google", "Chrome", "Application", "chrome.exe")}, nil)
	_, err := f.find()
	assert.ErrorIs(t, err, ErrExecutableNotFound)
}

func TestFindExecutableNotFound(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows", "plan9"} {
		_, err := fakeFinder(goos, nil, nil, nil).find()
		assert.ErrorIs(t, err, ErrExecutableNotFound, goos)
	}
}
