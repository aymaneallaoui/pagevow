package browser

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ErrExecutableNotFound reports that no installed Chromium or Chrome was found.
var ErrExecutableNotFound = errors.New("no Chromium or Chrome executable found on PATH or in the standard install locations")

type finder struct {
	goos     string
	lookPath func(string) (string, error)
	isFile   func(string) bool
	getenv   func(string) string
}

func systemFinder() finder {
	return finder{
		goos:     runtime.GOOS,
		lookPath: exec.LookPath,
		isFile:   regularFile,
		getenv:   os.Getenv,
	}
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// FindExecutable returns the path of an installed Chromium or Chrome for the current OS.
func FindExecutable() (string, error) {
	return systemFinder().find()
}

func (f finder) find() (string, error) {
	for _, name := range f.pathNames() {
		if path, err := f.lookPath(name); err == nil && path != "" {
			return path, nil
		}
	}
	for _, path := range f.installLocations() {
		if f.isFile(path) {
			return path, nil
		}
	}
	return "", ErrExecutableNotFound
}

func (f finder) pathNames() []string {
	switch f.goos {
	case "linux":
		return []string{"chromium", "chromium-browser", "google-chrome-stable", "google-chrome", "chrome"}
	case "windows":
		return []string{"chrome.exe", "chromium.exe"}
	default:
		return nil
	}
}

func (f finder) installLocations() []string {
	switch f.goos {
	case "linux":
		return []string{
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/lib/chromium/chromium",
			"/usr/lib/chromium-browser/chromium-browser",
			"/snap/bin/chromium",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/google-chrome",
			"/opt/google/chrome/chrome",
		}
	case "darwin":
		return f.darwinLocations()
	case "windows":
		return f.windowsLocations()
	default:
		return nil
	}
}

func (f finder) darwinLocations() []string {
	apps := []string{
		"Chromium.app/Contents/MacOS/Chromium",
		"Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
		"Google Chrome.app/Contents/MacOS/Google Chrome",
	}
	roots := []string{"/Applications"}
	if home := f.getenv("HOME"); home != "" {
		roots = append(roots, filepath.Join(home, "Applications"))
	}
	var out []string
	for _, app := range apps {
		for _, root := range roots {
			out = append(out, filepath.Join(root, app))
		}
	}
	return out
}

func (f finder) windowsLocations() []string {
	suffixes := []string{
		filepath.Join("Chromium", "Application", "chrome.exe"),
		filepath.Join("Google", "Chrome", "Application", "chrome.exe"),
	}
	var out []string
	for _, env := range []string{"LocalAppData", "ProgramFiles", "ProgramFiles(x86)"} {
		root := f.getenv(env)
		if root == "" {
			continue
		}
		for _, suffix := range suffixes {
			out = append(out, filepath.Join(root, suffix))
		}
	}
	return out
}
