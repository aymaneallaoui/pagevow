package browser

import (
	"context"
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
	recordFile = "installed.json"
	partSuffix = ".zip.part"
	tmpSuffix  = ".tmp"
)

// ErrNotInstalled reports that pagevow has not installed a browser.
var ErrNotInstalled = errors.New("pagevow has not installed a browser")

// ErrInstallBroken reports an install record that is unreadable or whose browser is gone.
var ErrInstallBroken = errors.New("the installed browser is missing or its record is invalid")

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
	Client     *http.Client
	Force      bool
	// Guard runs once, before any download, only when an install will happen; its error stops the install.
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

	if !opts.Force && regularFile(executable) {
		return alreadyInstalled(opts.BrowserDir, want, now())
	}
	if opts.Guard != nil {
		if err := opts.Guard(ctx); err != nil {
			return Installed{}, err
		}
	}
	if err := os.MkdirAll(opts.BrowserDir, 0o700); err != nil {
		return Installed{}, fmt.Errorf("install browser: create install directory: %w", err)
	}
	part := versionDir + partSuffix
	defer func() { _ = os.Remove(part) }()
	if err := download(ctx, opts, pin, part); err != nil {
		return Installed{}, err
	}
	if err := unpack(part, versionDir, pin); err != nil {
		return Installed{}, err
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

func alreadyInstalled(browserDir string, want Installed, now time.Time) (Installed, error) {
	if current, err := readRecord(browserDir); err == nil && current.Version == want.Version && current.Executable == want.Executable {
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

func download(ctx context.Context, opts InstallOptions, pin Pin, part string) error {
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	url := pin.URL(baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("download browser: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download browser from %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download browser from %s: unexpected HTTP status %s", url, resp.Status)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != pin.Size {
		return fmt.Errorf("download browser: the server announces %d bytes, expected %d", resp.ContentLength, pin.Size)
	}
	if err := os.Remove(part); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("download browser: remove leftover download: %w", err)
	}
	file, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the path is inside the install directory
	if err != nil {
		return fmt.Errorf("download browser: create download file: %w", err)
	}
	digest := sha256.New()
	if opts.Progress != nil {
		opts.Progress(0, pin.Size)
	}
	counter := &progressWriter{total: pin.Size, report: opts.Progress}
	copied, copyErr := io.Copy(io.MultiWriter(file, digest, counter), io.LimitReader(resp.Body, pin.Size+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("download browser from %s: %w", url, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("download browser: write download file: %w", closeErr)
	}
	if copied > pin.Size {
		return fmt.Errorf("download browser: size mismatch: the download is larger than the expected %d bytes", pin.Size)
	}
	if copied < pin.Size {
		return fmt.Errorf("download browser: size mismatch: expected %d bytes, got %d", pin.Size, copied)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != pin.SHA256 {
		return fmt.Errorf("download browser: checksum mismatch: expected sha256 %s, got %s", pin.SHA256, got)
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

func unpack(archive, versionDir string, pin Pin) error {
	staging := versionDir + tmpSuffix
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("install browser: remove leftover staging directory: %w", err)
	}
	if err := os.Mkdir(staging, dirMode); err != nil {
		return fmt.Errorf("install browser: create staging directory: %w", err)
	}
	if err := extractZip(archive, staging, maxExtractBytes); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("install browser: %w", err)
	}
	executable := filepath.Join(staging, filepath.FromSlash(pin.Executable))
	if !regularFile(executable) {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("install browser: the archive does not contain %s", pin.Executable)
	}
	if !strings.HasPrefix(pin.Platform, "win") {
		if err := os.Chmod(executable, execFileMode); err != nil {
			_ = os.RemoveAll(staging)
			return fmt.Errorf("install browser: make the browser executable: %w", err)
		}
	}
	if err := os.RemoveAll(versionDir); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("install browser: remove the previous install: %w", err)
	}
	if err := os.Rename(staging, versionDir); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("install browser: move the browser into place: %w", err)
	}
	return nil
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

// LookupInstalled returns the record of the browser that pagevow installed in browserDir, checked against the disk.
func LookupInstalled(browserDir string) (Installed, error) {
	if browserDir == "" {
		return Installed{}, ErrNotInstalled
	}
	rec, err := readRecord(browserDir)
	if errors.Is(err, fs.ErrNotExist) {
		return Installed{}, ErrNotInstalled
	}
	if err != nil {
		return Installed{}, fmt.Errorf("read the install record: %w: %w", ErrInstallBroken, err)
	}
	if !insideDir(browserDir, rec.Executable) {
		return Installed{}, fmt.Errorf("the recorded executable %s is outside %s: %w", rec.Executable, browserDir, ErrInstallBroken)
	}
	if !regularFile(rec.Executable) {
		return Installed{}, fmt.Errorf("the recorded executable %s does not exist: %w", rec.Executable, ErrInstallBroken)
	}
	return rec, nil
}

func insideDir(dir, target string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil || !filepath.IsAbs(target) {
		return false
	}
	rel, err := filepath.Rel(absDir, target)
	return err == nil && filepath.IsLocal(rel)
}
