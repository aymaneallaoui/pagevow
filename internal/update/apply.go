package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	windows          = "windows"
	checksumsName    = "checksums.txt"
	defaultMaxBytes  = 200 << 20
	maxChecksumBytes = 1 << 20
	defaultBinMode   = 0o755
	fallbackDirMode  = 0o700
)

// ErrNoAsset reports that a release has no archive for the platform or no checksum file.
var ErrNoAsset = errors.New("the release has no matching asset")

// ErrChecksumMismatch reports a download whose SHA-256 differs from the release checksum file.
var ErrChecksumMismatch = errors.New("checksum mismatch")

// ErrNotReplaced reports that the new binary was verified but left in Result.Staged because the executable could not be replaced.
var ErrNotReplaced = errors.New("the executable was not replaced")

// Result describes what Apply did to the executable.
type Result struct {
	From       string
	To         string
	Executable string
	Skipped    bool
	Staged     string
}

// Apply downloads the release archive for opts.GOOS and opts.GOARCH, verifies its SHA-256 and replaces opts.Executable, unless it is already current and Force is unset.
func Apply(ctx context.Context, opts Options, rel Release) (Result, error) {
	if opts.Executable == "" || opts.GOOS == "" || opts.GOARCH == "" {
		return Result{}, errors.New("update: executable, GOOS and GOARCH are required")
	}
	exe, err := filepath.EvalSymlinks(opts.Executable)
	if err != nil {
		return Result{}, fmt.Errorf("resolve the executable %s: %w", opts.Executable, err)
	}
	RemoveLeftover(exe, opts.GOOS)
	result := Result{From: opts.Current, To: rel.Version, Executable: exe}
	newer, err := NewerThan(rel.Version, opts.Current)
	if err != nil {
		return Result{}, err
	}
	if !newer && !opts.Force {
		result.Skipped = true
		return result, nil
	}
	archive, sums, err := opts.pick(rel)
	if err != nil {
		return Result{}, err
	}
	want, err := opts.expectedSum(ctx, sums, archive.Name)
	if err != nil {
		return Result{}, err
	}
	staging, err := chooseStaging(exe, opts.FallbackDir)
	if err != nil {
		return Result{}, err
	}
	fresh, err := opts.fetchBinary(ctx, archive, want, staging.dir, executableMode(exe))
	if err != nil {
		return Result{}, err
	}
	kept := filepath.Join(staging.dir, "pagevow-"+rel.Version+binarySuffix(opts.GOOS))
	if staging.cause != nil {
		return stage(result, fresh, kept, staging.cause)
	}
	if err := replaceExecutable(opts.GOOS, exe, fresh); err != nil {
		return keepAfterFailure(result, fresh, opts, rel.Version, err)
	}
	return result, nil
}

func stage(result Result, fresh, target string, cause error) (Result, error) {
	if err := moveFile(fresh, target); err != nil {
		_ = os.Remove(fresh)
		return Result{}, fmt.Errorf("keep the new binary at %s: %w", target, err)
	}
	result.Staged = target
	return result, fmt.Errorf("%w: %w", ErrNotReplaced, cause)
}

func keepAfterFailure(result Result, fresh string, opts Options, version string, cause error) (Result, error) {
	if opts.FallbackDir == "" {
		_ = os.Remove(fresh)
		return Result{}, cause
	}
	if err := os.MkdirAll(opts.FallbackDir, fallbackDirMode); err != nil {
		_ = os.Remove(fresh)
		return Result{}, errors.Join(cause, fmt.Errorf("create the fallback directory %s: %w", opts.FallbackDir, err))
	}
	target := filepath.Join(opts.FallbackDir, "pagevow-"+version+binarySuffix(opts.GOOS))
	staged, err := stage(result, fresh, target, cause)
	if errors.Is(err, ErrNotReplaced) {
		return staged, err
	}
	return Result{}, errors.Join(cause, err)
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	return copyAndRemove(src, dst)
}

func copyAndRemove(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // src is the verified file this package just wrote
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	out, err := os.CreateTemp(filepath.Dir(dst), ".pagevow-keep-*")
	if err != nil {
		return fmt.Errorf("create a file next to %s: %w", dst, err)
	}
	tmp := out.Name()
	_, copyErr := io.Copy(out, in)
	var syncErr error
	if copyErr == nil {
		syncErr = out.Sync()
	}
	closeErr := out.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("copy %s: %w", src, err)
	}
	if err := os.Chmod(tmp, info.Mode().Perm()); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("make %s executable: %w", dst, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("move the copy to %s: %w", dst, err)
	}
	_ = os.Remove(src)
	return nil
}

func (o Options) pick(rel Release) (archive, sums Asset, err error) {
	name := archiveName(rel.Version, o.GOOS, o.GOARCH)
	for _, asset := range rel.Assets {
		switch asset.Name {
		case name:
			archive = asset
		case checksumsName:
			sums = asset
		}
	}
	if archive.Name == "" {
		return Asset{}, Asset{}, fmt.Errorf("%w: no %s for %s/%s", ErrNoAsset, name, o.GOOS, o.GOARCH)
	}
	if sums.Name == "" {
		return Asset{}, Asset{}, fmt.Errorf("%w: no %s to verify %s against", ErrNoAsset, checksumsName, name)
	}
	return archive, sums, nil
}

func archiveName(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == windows {
		ext = "zip"
	}
	return fmt.Sprintf("pagevow_%s_%s_%s.%s", version, goos, goarch, ext)
}

func binarySuffix(goos string) string {
	if goos == windows {
		return ".exe"
	}
	return ""
}

func (o Options) archiveLimit() int64 {
	if o.maxArchive > 0 {
		return o.maxArchive
	}
	return defaultMaxBytes
}

func (o Options) binaryLimit() int64 {
	if o.maxBinary > 0 {
		return o.maxBinary
	}
	return defaultMaxBytes
}

func (o Options) openAsset(ctx context.Context, asset Asset) (*http.Response, error) {
	if err := o.checkAssetURL(asset); err != nil {
		return nil, err
	}
	resp, err := o.get(ctx, asset.APIURL, "application/octet-stream")
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("download %s: %w", asset.Name, statusError(resp, o.Token != ""))
	}
	return resp, nil
}

func (o Options) expectedSum(ctx context.Context, sums Asset, archiveName string) (string, error) {
	resp, err := o.openAsset(ctx, sums)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumBytes+1))
	if err != nil {
		return "", fmt.Errorf("download %s: %w", sums.Name, err)
	}
	if len(data) > maxChecksumBytes {
		return "", fmt.Errorf("%s is larger than %d bytes", sums.Name, maxChecksumBytes)
	}
	return findChecksum(string(data), archiveName)
}

func findChecksum(text, name string) (string, error) {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		sum := strings.ToLower(fields[0])
		if raw, err := hex.DecodeString(sum); err != nil || len(raw) != sha256.Size {
			return "", fmt.Errorf("%s has an invalid SHA-256 for %s", checksumsName, name)
		}
		return sum, nil
	}
	return "", fmt.Errorf("%s has no entry for %s", checksumsName, name)
}

func (o Options) fetchBinary(ctx context.Context, archive Asset, want, dir string, mode os.FileMode) (string, error) {
	part, err := os.CreateTemp(dir, ".pagevow-update-*.part")
	if err != nil {
		return "", fmt.Errorf("create the download file: %w", err)
	}
	defer func() { _ = os.Remove(part.Name()) }()
	sum, downloadErr := o.download(ctx, archive, part)
	closeErr := part.Close()
	if downloadErr != nil {
		return "", downloadErr
	}
	if closeErr != nil {
		return "", fmt.Errorf("write the download file: %w", closeErr)
	}
	if sum != want {
		return "", fmt.Errorf("%w: %s has sha256 %s, %s lists %s", ErrChecksumMismatch, archive.Name, sum, checksumsName, want)
	}
	return o.unpack(part.Name(), dir, mode)
}

func (o Options) download(ctx context.Context, archive Asset, dst io.Writer) (string, error) {
	limit := o.archiveLimit()
	if archive.Size > limit {
		return "", fmt.Errorf("download %s: %d bytes is more than the limit of %d", archive.Name, archive.Size, limit)
	}
	resp, err := o.openAsset(ctx, archive)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	total := archive.Size
	if total <= 0 {
		total = resp.ContentLength
	}
	if o.Progress != nil {
		o.Progress(0, total)
	}
	digest := sha256.New()
	counter := &progressWriter{total: total, report: o.Progress}
	copied, err := io.Copy(io.MultiWriter(dst, digest, counter), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", fmt.Errorf("download %s: %w", archive.Name, err)
	}
	if copied > limit {
		return "", fmt.Errorf("download %s: more than the limit of %d bytes", archive.Name, limit)
	}
	if archive.Size > 0 && copied != archive.Size {
		return "", fmt.Errorf("download %s: size mismatch: expected %d bytes, got %d", archive.Name, archive.Size, copied)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
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

func (o Options) unpack(archivePath, dir string, mode os.FileMode) (string, error) {
	fresh, err := os.CreateTemp(dir, ".pagevow-new-*")
	if err != nil {
		return "", fmt.Errorf("create the new binary: %w", err)
	}
	name := fresh.Name()
	extractErr := extractBinary(archivePath, o.GOOS, fresh, o.binaryLimit())
	var syncErr error
	if extractErr == nil {
		if err := fresh.Sync(); err != nil {
			syncErr = fmt.Errorf("sync the new binary: %w", err)
		}
	}
	closeErr := fresh.Close()
	if err := errors.Join(extractErr, syncErr, closeErr); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	if err := os.Chmod(name, mode); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("make the new binary executable: %w", err)
	}
	return name, nil
}

func executableMode(exe string) os.FileMode {
	info, err := os.Stat(exe)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		return defaultBinMode
	}
	return info.Mode().Perm()
}

type staging struct {
	dir   string
	cause error
}

func chooseStaging(exe, fallback string) (staging, error) {
	dir := filepath.Dir(exe)
	probe, err := os.CreateTemp(dir, ".pagevow-probe-*")
	if err == nil {
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
		return staging{dir: dir}, nil
	}
	if fallback == "" {
		return staging{}, fmt.Errorf("the directory of %s is not writable: %w", exe, err)
	}
	if mkErr := os.MkdirAll(fallback, fallbackDirMode); mkErr != nil {
		return staging{}, fmt.Errorf("the directory of %s is not writable (%w) and the fallback directory failed: %w", exe, err, mkErr)
	}
	return staging{dir: fallback, cause: fmt.Errorf("the directory of %s is not writable: %w", exe, err)}, nil
}
