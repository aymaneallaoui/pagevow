package browser

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	recordFile  = "installed.json"
	lockFile    = ".install.lock"
	partPrefix  = "download-"
	partSuffix  = ".zip.part"
	stagePrefix = "staging-"
	tmpSuffix   = ".tmp"
	oldPrefix   = "old-"
)

// ErrNotInstalled reports that pagevow has not installed a browser.
var ErrNotInstalled = errors.New("pagevow has not installed a browser")

// ErrInstallBroken reports an install record that is unreadable, made for another platform, or whose browser is gone.
var ErrInstallBroken = errors.New("the installed browser is missing or its record is invalid")

// ErrInstallRunning reports that another install holds the lock of the install directory.
var ErrInstallRunning = errors.New("another pagevow install is running")

// Installed is the record of a browser that pagevow installed.
type Installed struct {
	Version          string    `json:"version"`
	Platform         string    `json:"platform"`
	Executable       string    `json:"executable"`
	InstalledAt      time.Time `json:"installed_at"`
	AlreadyInstalled bool      `json:"-"`
}

// InstallOptions configures Install; BrowserDir is the directory that holds the versions and the record.
type InstallOptions struct {
	BrowserDir string
	GOOS       string
	GOARCH     string
	BaseURL    string
	// Client performs the download; Install fails when it is nil.
	Client *http.Client
	Force  bool
	// Guard runs before the download and again right before the new files replace the old ones, only when an install
	// will happen; its error stops the install.
	Guard func(ctx context.Context) error
	// Progress is called with the bytes downloaded so far and the total, first with zero bytes.
	Progress func(done, total int64)
	Now      func() time.Time
	// Pin replaces the pinned archive; tests use it to serve a small archive.
	Pin *Pin
}

// Install downloads the pinned Chrome for Testing build, verifies its size and SHA-256 and unpacks it next to the record.
func Install(ctx context.Context, opts InstallOptions) (Installed, error) {
	if opts.BrowserDir == "" {
		return Installed{}, errors.New("install browser: no install directory")
	}
	pin, err := opts.pin()
	if err != nil {
		return Installed{}, fmt.Errorf("install browser: %w", err)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	versionDir := filepath.Join(opts.BrowserDir, PinnedVersion)
	executable := filepath.Join(versionDir, filepath.FromSlash(pin.Executable))
	want := Installed{Version: PinnedVersion, Platform: pin.Platform, Executable: executable}

	if !opts.Force && verifyExecutable(opts.BrowserDir, executable) == nil {
		return alreadyInstalled(opts.BrowserDir, want, now())
	}
	if opts.Client == nil {
		return Installed{}, errors.New("install browser: no HTTP client")
	}
	if opts.Guard != nil {
		if err := opts.Guard(ctx); err != nil {
			return Installed{}, err
		}
	}
	if err := os.MkdirAll(opts.BrowserDir, 0o700); err != nil {
		return Installed{}, fmt.Errorf("install browser: create install directory: %w", err)
	}
	unlock, err := acquireInstallLock(opts.BrowserDir)
	if err != nil {
		return Installed{}, err
	}
	defer unlock()
	if !opts.Force && verifyExecutable(opts.BrowserDir, executable) == nil {
		return alreadyInstalled(opts.BrowserDir, want, now())
	}
	sweepLeftovers(opts.BrowserDir)

	archive, err := download(ctx, opts, pin)
	if err != nil {
		return Installed{}, err
	}
	defer func() {
		_ = archive.Close()
		_ = os.Remove(archive.Name())
	}()
	staging, err := stage(ctx, archive, opts.BrowserDir, pin)
	if err != nil {
		return Installed{}, err
	}
	defer func() { _ = os.RemoveAll(staging) }()

	if opts.Guard != nil {
		if err := opts.Guard(ctx); err != nil {
			return Installed{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Installed{}, fmt.Errorf("install browser: %w", err)
	}
	previous, err := swapIn(os.Rename, staging, versionDir)
	if err != nil {
		return Installed{}, err
	}
	if previous != "" {
		defer func() { _ = os.RemoveAll(previous) }()
	}
	want.InstalledAt = now().UTC()
	if err := writeRecord(opts.BrowserDir, want); err != nil {
		return Installed{}, err
	}
	return want, nil
}

func (o InstallOptions) pin() (Pin, error) {
	if o.Pin != nil {
		return *o.Pin, nil
	}
	return PinFor(o.GOOS, o.GOARCH)
}

func acquireInstallLock(browserDir string) (func(), error) {
	file, err := lockFileExclusive(filepath.Join(browserDir, lockFile))
	switch {
	case errors.Is(err, ErrInstallRunning):
		return nil, fmt.Errorf("install browser: %w in %s: wait for it to finish", err, browserDir)
	case err != nil:
		return nil, fmt.Errorf("install browser: lock the install directory: %w", err)
	}
	return func() { _ = file.Close() }, nil
}

// sweepLeftovers removes what an interrupted install left behind; the caller holds the install lock.
func sweepLeftovers(browserDir string) {
	entries, err := os.ReadDir(browserDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		leftover := strings.HasPrefix(name, partPrefix) && strings.HasSuffix(name, partSuffix) ||
			strings.HasPrefix(name, stagePrefix) && strings.HasSuffix(name, tmpSuffix) ||
			strings.HasPrefix(name, oldPrefix)
		if leftover {
			_ = os.RemoveAll(filepath.Join(browserDir, name))
		}
	}
}

func alreadyInstalled(browserDir string, want Installed, now time.Time) (Installed, error) {
	if current, err := readRecord(browserDir); err == nil && current.Version == want.Version &&
		current.Platform == want.Platform && current.Executable == want.Executable {
		current.AlreadyInstalled = true
		return current, nil
	}
	want.InstalledAt = now.UTC()
	if err := writeRecord(browserDir, want); err != nil {
		return Installed{}, err
	}
	want.AlreadyInstalled = true
	return want, nil
}

// download fetches the archive into a fresh temporary file and returns it open, after its size and SHA-256 matched.
// The caller closes and removes the file; on an error download has already done both.
func download(ctx context.Context, opts InstallOptions, pin Pin) (*os.File, error) {
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	url := pin.URL(baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("download browser: %w", err)
	}
	resp, err := opts.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download browser from %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download browser from %s: unexpected HTTP status %s", url, resp.Status)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != pin.Size {
		return nil, fmt.Errorf("download browser: the server announces %d bytes, expected %d", resp.ContentLength, pin.Size)
	}
	file, err := os.CreateTemp(opts.BrowserDir, partPrefix+"*"+partSuffix)
	if err != nil {
		return nil, fmt.Errorf("download browser: create download file: %w", err)
	}
	if err := fill(file, resp.Body, opts, pin); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, fmt.Errorf("download browser from %s: %w", url, err)
	}
	return file, nil
}

func fill(file *os.File, body io.Reader, opts InstallOptions, pin Pin) error {
	digest := sha256.New()
	if opts.Progress != nil {
		opts.Progress(0, pin.Size)
	}
	counter := &progressWriter{total: pin.Size, report: opts.Progress}
	copied, err := io.Copy(io.MultiWriter(file, digest, counter), io.LimitReader(body, pin.Size+1))
	if err != nil {
		return err
	}
	switch {
	case copied > pin.Size:
		return fmt.Errorf("size mismatch: expected %d bytes, got at least %d", pin.Size, copied)
	case copied < pin.Size:
		return fmt.Errorf("size mismatch: expected %d bytes, got %d", pin.Size, copied)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != pin.SHA256 {
		return fmt.Errorf("checksum mismatch: expected sha256 %s, got %s", pin.SHA256, got)
	}
	return nil
}

type progressWriter struct {
	done   int64
	total  int64
	report func(done, total int64)
}

func (p *progressWriter) Write(data []byte) (int, error) {
	p.done += int64(len(data))
	if p.report != nil {
		p.report(p.done, p.total)
	}
	return len(data), nil
}

// stage unpacks the hashed archive into a fresh staging directory and checks that the browser is a regular file in it.
func stage(ctx context.Context, archive *os.File, browserDir string, pin Pin) (string, error) {
	info, err := archive.Stat()
	if err != nil {
		return "", fmt.Errorf("install browser: stat the download: %w", err)
	}
	reader, err := zip.NewReader(archive, info.Size())
	if err != nil {
		return "", fmt.Errorf("install browser: open archive: %w", err)
	}
	staging, err := os.MkdirTemp(browserDir, stagePrefix+"*"+tmpSuffix)
	if err != nil {
		return "", fmt.Errorf("install browser: create staging directory: %w", err)
	}
	if err := extractZip(ctx, reader, staging, maxExtractBytes); err != nil {
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("install browser: %w", err)
	}
	if err := prepareExecutable(staging, pin); err != nil {
		_ = os.RemoveAll(staging)
		return "", err
	}
	return staging, nil
}

func prepareExecutable(staging string, pin Pin) error {
	root, err := os.OpenRoot(staging)
	if err != nil {
		return fmt.Errorf("install browser: open staging directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	rel := filepath.FromSlash(pin.Executable)
	info, err := root.Lstat(rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("install browser: the archive does not contain %s", pin.Executable)
	case err != nil:
		return fmt.Errorf("install browser: check %s: %w", pin.Executable, err)
	case !info.Mode().IsRegular():
		return fmt.Errorf("install browser: %s in the archive is not a regular file", pin.Executable)
	}
	if strings.HasPrefix(pin.Platform, "win") {
		return nil
	}
	if err := root.Chmod(rel, execFileMode); err != nil {
		return fmt.Errorf("install browser: make the browser executable: %w", err)
	}
	return nil
}

// swapIn moves staging to versionDir. An existing versionDir is first renamed aside and returned, and is put back when
// the move fails, so a failed swap leaves the previous install in place.
func swapIn(rename func(oldPath, newPath string) error, staging, versionDir string) (previous string, err error) {
	if _, err := os.Lstat(versionDir); err == nil {
		previous = filepath.Join(filepath.Dir(versionDir), oldPrefix+filepath.Base(versionDir)+"-"+rand.Text())
		if err := rename(versionDir, previous); err != nil {
			return "", fmt.Errorf("install browser: move the previous install aside: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("install browser: check the previous install: %w", err)
	}
	if err := rename(staging, versionDir); err != nil {
		err = fmt.Errorf("install browser: move the browser into place: %w", err)
		if previous != "" {
			if restoreErr := rename(previous, versionDir); restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("restore the previous install from %s: %w", previous, restoreErr))
			}
		}
		return "", err
	}
	return previous, nil
}

func readRecord(browserDir string) (Installed, error) {
	data, err := os.ReadFile(filepath.Join(browserDir, recordFile)) //nolint:gosec // the path is inside the install directory
	if err != nil {
		return Installed{}, err
	}
	var rec Installed
	if err := json.Unmarshal(data, &rec); err != nil {
		return Installed{}, fmt.Errorf("decode %s: %w", recordFile, err)
	}
	return rec, nil
}

func writeRecord(browserDir string, rec Installed) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("install browser: encode record: %w", err)
	}
	tmp, err := os.CreateTemp(browserDir, "installed-*.tmp")
	if err != nil {
		return fmt.Errorf("install browser: write record: %w", err)
	}
	_, writeErr := tmp.Write(append(data, '\n'))
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("install browser: write record: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(browserDir, recordFile)); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("install browser: write record: %w", err)
	}
	return nil
}

// LookupInstalled returns the record of the browser that pagevow installed in browserDir for goos and goarch, checked against the disk.
func LookupInstalled(browserDir, goos, goarch string) (Installed, error) {
	if browserDir == "" {
		return Installed{}, ErrNotInstalled
	}
	platform, err := PlatformName(goos, goarch)
	if err != nil {
		return Installed{}, ErrNotInstalled
	}
	rec, err := readRecord(browserDir)
	if errors.Is(err, fs.ErrNotExist) {
		return Installed{}, ErrNotInstalled
	}
	if err != nil {
		return Installed{}, fmt.Errorf("read the install record: %w: %w", ErrInstallBroken, err)
	}
	if rec.Platform != platform {
		return Installed{}, fmt.Errorf("the installed browser is for %q, this system needs %q: %w", rec.Platform, platform, ErrInstallBroken)
	}
	if err := verifyExecutable(browserDir, rec.Executable); err != nil {
		return Installed{}, fmt.Errorf("%w: %w", err, ErrInstallBroken)
	}
	return rec, nil
}

// verifyExecutable checks that executable is a regular file whose real path, links resolved, is inside browserDir.
func verifyExecutable(browserDir, executable string) error {
	if !insideDir(browserDir, executable) {
		return fmt.Errorf("the recorded executable %s is outside %s", executable, browserDir)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("the recorded executable %s does not exist", executable)
	}
	realDir, err := filepath.EvalSymlinks(browserDir)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", browserDir, err)
	}
	if !insideDir(realDir, resolved) {
		return fmt.Errorf("the recorded executable %s resolves to %s, outside %s", executable, resolved, browserDir)
	}
	if !regularFile(resolved) {
		return fmt.Errorf("the recorded executable %s is not a regular file", executable)
	}
	return nil
}

func insideDir(dir, target string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil || !filepath.IsAbs(target) {
		return false
	}
	rel, err := filepath.Rel(absDir, target)
	return err == nil && filepath.IsLocal(rel)
}
