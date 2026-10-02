package model

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxBytes int64 = 20 << 30
	maxSourceFiles        = 100000

	runsMode    = 0o750
	subdirMode  = 0o700
	fileMode    = 0o600
	stagePrefix = ".staging-"
	oldPrefix   = ".old-"
	staleAfter  = 24 * time.Hour
)

// ErrExists reports a model directory that the install would have to replace and may not.
var ErrExists = errors.New("model directory already exists")

// ErrTooLarge reports a model above the size limit.
var ErrTooLarge = errors.New("model is larger than the install limit")

// Plan is what an install is about to do, reported before the first byte moves.
type Plan struct {
	Source   string
	Revision string
	Dir      string
	Files    int
	Bytes    int64
	Link     bool
}

// Options configures Install.
type Options struct {
	// RunsDir is the directory that holds the run directories; the model lands in RunsDir/Name.
	RunsDir string
	// Name defaults to the last element of the path or the repository name.
	Name string
	// Force replaces a model that pagevow installed; a directory without a pagevow record is never replaced.
	Force bool
	// Link makes RunsDir/Name a symbolic link to a directory source instead of a copy.
	Link bool
	// HubBaseURL defaults to DefaultHubURL; tests replace it.
	HubBaseURL string
	// Client performs the hub requests; Install of a repository fails when it is nil.
	Client *http.Client
	Token  string
	// StallTimeout fails a download that receives no bytes for this long; zero means 2 minutes.
	StallTimeout time.Duration
	// MaxBytes caps the bytes that an install copies or downloads; zero means 20 GiB.
	MaxBytes int64
	// Guard runs when an existing model would be replaced, before any bytes move and again right before the swap.
	Guard func(ctx context.Context, dir string) error
	// Begin is called once, after the source is checked and before any file moves, unless the model is already installed.
	Begin func(Plan)
	// Progress is called with a file, the bytes of it done so far and its size, first with zero bytes.
	Progress func(file string, done, total int64)
	Now      func() time.Time
}

type installer struct {
	src    Source
	opts   Options
	name   string
	target string
	limit  int64
	commit string
}

// Install puts the model of src into opts.RunsDir under its name and returns its record.
func Install(ctx context.Context, src Source, opts Options) (Installed, error) {
	if opts.RunsDir == "" {
		return Installed{}, errors.New("install model: no runs directory")
	}
	if err := src.check(opts.Link); err != nil {
		return Installed{}, err
	}
	if src.Path != "" {
		abs, err := filepath.Abs(src.Path)
		if err != nil {
			return Installed{}, fmt.Errorf("install model: resolve %s: %w", src.Path, err)
		}
		src.Path = abs
		if opts.Link {
			if src.Path, err = linkSource(abs, opts.RunsDir); err != nil {
				return Installed{}, err
			}
		}
	}
	name := opts.Name
	if name == "" {
		name = src.defaultName()
	}
	if err := checkName(name); err != nil {
		return Installed{}, err
	}
	in := &installer{src: src, opts: opts, name: name, target: filepath.Join(opts.RunsDir, name), limit: opts.MaxBytes}
	if in.limit <= 0 {
		in.limit = defaultMaxBytes
	}
	if src.Repo != "" {
		return in.fromHub(ctx)
	}
	return in.fromPath(ctx)
}

// linkSource returns the real path of a directory to link; a directory inside the runs directory is refused.
func linkSource(abs, runsDir string) (string, error) {
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("install model: resolve %s: %w", abs, err)
	}
	if within(runsDir, abs) || within(runsDir, resolved) {
		return "", fmt.Errorf("%w: %s lies inside the runs directory %s; install it without --link to copy it", ErrInvalidSource, abs, runsDir)
	}
	return resolved, nil
}

func (in *installer) fromPath(ctx context.Context) (Installed, error) {
	base, err := validate(in.src.Path, !in.opts.Link)
	if err != nil {
		return Installed{}, err
	}
	limit := in.limit
	if in.opts.Link {
		limit = math.MaxInt64
	}
	files, total, err := walkSource(in.src.Path, limit, in.opts.Link)
	if err != nil {
		return Installed{}, err
	}
	already, err := in.existing(ctx, files)
	if err != nil {
		return Installed{}, err
	}
	if already != nil {
		return *already, nil
	}
	in.begin(len(files), total)
	if in.opts.Link {
		return in.link(ctx, base, files)
	}
	return in.copyTree(ctx, base, files)
}

func (in *installer) fromHub(ctx context.Context) (Installed, error) {
	hub, err := newHub(in.opts)
	if err != nil {
		return Installed{}, err
	}
	if in.commit, err = hub.commit(ctx, in.src.Repo, in.src.Revision); err != nil {
		return Installed{}, err
	}
	already, err := in.existing(ctx, nil)
	if err != nil {
		return Installed{}, err
	}
	if already != nil {
		return *already, nil
	}
	files, total, err := hub.files(ctx, in.src.Repo, in.commit, in.limit)
	if err != nil {
		return Installed{}, err
	}
	in.begin(len(files), total)
	staging, err := in.stagingDir()
	if err != nil {
		return Installed{}, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	root, err := os.OpenRoot(staging)
	if err != nil {
		return Installed{}, fmt.Errorf("install model: open the staging directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	records := make([]FileRecord, 0, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return Installed{}, fmt.Errorf("install model: %w", err)
		}
		in.progress(file.path, 0, file.size)
		record, err := hub.fetch(ctx, in.src.Repo, in.commit, file, root, in.reporter(file.path, file.size))
		if err != nil {
			return Installed{}, err
		}
		records = append(records, record)
	}
	return in.publish(ctx, staging, root, records)
}

func (in *installer) copyTree(ctx context.Context, _ string, files []sourceFile) (Installed, error) {
	staging, err := in.stagingDir()
	if err != nil {
		return Installed{}, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	from, err := os.OpenRoot(in.src.Path)
	if err != nil {
		return Installed{}, fmt.Errorf("install model: open %s: %w", in.src.Path, err)
	}
	defer func() { _ = from.Close() }()
	to, err := os.OpenRoot(staging)
	if err != nil {
		return Installed{}, fmt.Errorf("install model: open the staging directory: %w", err)
	}
	defer func() { _ = to.Close() }()
	records := make([]FileRecord, 0, len(files))
	remaining := in.limit
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return Installed{}, fmt.Errorf("install model: %w", err)
		}
		record, err := in.copyFile(ctx, from, to, file, remaining)
		if err != nil {
			return Installed{}, err
		}
		remaining -= record.Size
		records = append(records, record)
	}
	return in.publish(ctx, staging, to, records)
}

func (in *installer) copyFile(ctx context.Context, from, to *os.Root, file sourceFile, remaining int64) (FileRecord, error) {
	in.progress(file.rel, 0, file.size)
	src, err := from.Open(filepath.FromSlash(file.rel))
	if err != nil {
		return FileRecord{}, fmt.Errorf("copy %s: %w", file.rel, err)
	}
	defer func() { _ = src.Close() }()
	written, sum, err := stageFile(ctx, to, file.rel, src, remaining, in.reporter(file.rel, file.size))
	switch {
	case err != nil:
		return FileRecord{}, fmt.Errorf("copy %s: %w", file.rel, err)
	case written > remaining:
		return FileRecord{}, fmt.Errorf("%w: %s holds more than %d bytes", ErrTooLarge, in.src.Path, in.limit)
	}
	return FileRecord{Name: file.rel, Size: written, SHA256: sum, ModTime: file.mtime.UTC()}, nil
}

// publish validates the staged files, records them and moves the staging directory into place.
func (in *installer) publish(ctx context.Context, staging string, root *os.Root, records []FileRecord) (Installed, error) {
	if err := root.Close(); err != nil {
		return Installed{}, fmt.Errorf("install model: close the staging directory: %w", err)
	}
	base, err := Validate(staging)
	if err != nil {
		return Installed{}, err
	}
	rec := in.record(base, records)
	if err := writeRecord(filepath.Join(staging, recordFile), rec); err != nil {
		return Installed{}, err
	}
	if err := in.place(ctx, staging); err != nil {
		return Installed{}, err
	}
	if err := os.Remove(linkRecordPath(in.opts.RunsDir, in.name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Installed{}, fmt.Errorf("install model: remove the record of the replaced link: %w", err)
	}
	rec.Dir = in.target
	return rec, nil
}

func (in *installer) link(ctx context.Context, base string, files []sourceFile) (Installed, error) {
	records := make([]FileRecord, len(files))
	for i, file := range files {
		records[i] = FileRecord{Name: file.rel, Size: file.size}
	}
	rec := in.record(base, records)
	if err := in.prepareRuns(); err != nil {
		return Installed{}, err
	}
	pending, err := stageRecord(in.opts.RunsDir, rec)
	if err != nil {
		return Installed{}, err
	}
	defer func() { _ = os.Remove(pending) }()
	staged := filepath.Join(in.opts.RunsDir, stagePrefix+in.name+"-"+rand.Text())
	if err := os.Symlink(in.src.Path, staged); err != nil {
		return Installed{}, symlinkError(runtime.GOOS, err)
	}
	defer func() { _ = os.Remove(staged) }()
	if err := in.place(ctx, staged); err != nil {
		return Installed{}, err
	}
	if err := os.Rename(pending, linkRecordPath(in.opts.RunsDir, in.name)); err != nil {
		_ = os.Remove(in.target)
		return Installed{}, fmt.Errorf("install model: record the link, which was removed again: %w", err)
	}
	rec.Dir, rec.Link = in.target, true
	return rec, nil
}

func symlinkError(goos string, err error) error {
	if goos == "windows" {
		return fmt.Errorf("install model: create the symbolic link (Windows allows it with Developer Mode on or the symlink privilege; leave out --link to copy the model): %w", err)
	}
	return fmt.Errorf("install model: create the symbolic link: %w", err)
}

func (in *installer) record(base string, files []FileRecord) Installed {
	return Installed{
		Name: in.name, Source: in.location(), Revision: in.commit, BaseModel: base, Files: files, InstalledAt: in.now().UTC(),
	}
}

func (in *installer) now() time.Time {
	if in.opts.Now == nil {
		return time.Now()
	}
	return in.opts.Now()
}

func (in *installer) location() string {
	if in.src.Path != "" {
		return in.src.Path
	}
	return in.src.Repo
}

func (in *installer) begin(files int, total int64) {
	if in.opts.Begin != nil {
		in.opts.Begin(Plan{Source: in.location(), Revision: in.commit, Dir: in.target, Files: files, Bytes: total, Link: in.opts.Link})
	}
}

func (in *installer) progress(file string, done, total int64) {
	if in.opts.Progress != nil {
		in.opts.Progress(file, done, total)
	}
}

func (in *installer) reporter(file string, total int64) func(done int64) {
	if in.opts.Progress == nil {
		return nil
	}
	return func(done int64) { in.opts.Progress(file, done, total) }
}

// existing decides what an existing target means: a record when the same model is installed, an error when the install may not replace it.
func (in *installer) existing(ctx context.Context, files []sourceFile) (*Installed, error) {
	info, err := os.Lstat(in.target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("install model: check %s: %w", in.target, err)
	}
	if in.sourceIsTarget(info.IsDir()) {
		return nil, fmt.Errorf("%w: the source %s is, or sits inside, %s", ErrExists, in.src.Path, in.target)
	}
	rec, err := ReadRecord(in.target)
	if err != nil {
		return nil, fmt.Errorf("%w: %s was not installed by pagevow, so pagevow does not replace it; move or remove it, or pass --name", ErrExists, in.target)
	}
	if in.opts.Force {
		return nil, in.guard(ctx)
	}
	switch {
	case rec.Source != in.location() || rec.Revision != in.commit || rec.Link != in.opts.Link:
		return nil, fmt.Errorf("%w: %s is installed from %s; pass --force to replace it", ErrExists, in.target, rec.Source)
	case in.src.Path != "" && !in.opts.Link && !sameFiles(rec.Files, files):
		return nil, fmt.Errorf("%w: the source %s changed since it was installed at %s; pass --force to install it again", ErrExists, in.src.Path, in.target)
	}
	if _, err := Validate(in.target); err != nil {
		return nil, fmt.Errorf("%w: %s is installed from %s but is incomplete; pass --force to install it again", ErrExists, in.target, rec.Source)
	}
	rec.AlreadyInstalled = true
	return &rec, nil
}

// sourceIsTarget reports a path source that is the target or lies inside it; a linked target is compared by its own path, not where it points.
func (in *installer) sourceIsTarget(targetIsDir bool) bool {
	if in.src.Path == "" {
		return false
	}
	return lexicallyInside(in.target, in.src.Path) || targetIsDir && sameOrInside(in.target, in.src.Path)
}

// sameFiles reports whether a copy record still describes the source files by name, size and modification time.
func sameFiles(recorded []FileRecord, files []sourceFile) bool {
	if len(recorded) != len(files) {
		return false
	}
	byName := make(map[string]FileRecord, len(recorded))
	for _, rec := range recorded {
		byName[rec.Name] = rec
	}
	for _, file := range files {
		rec, ok := byName[file.rel]
		if !ok || rec.Size != file.size || !rec.ModTime.Equal(file.mtime) {
			return false
		}
	}
	return true
}

func (in *installer) guard(ctx context.Context) error {
	if in.opts.Guard == nil {
		return nil
	}
	return in.opts.Guard(ctx, in.target)
}

func (in *installer) prepareRuns() error {
	if err := os.MkdirAll(in.opts.RunsDir, runsMode); err != nil {
		return fmt.Errorf("install model: create %s: %w", in.opts.RunsDir, err)
	}
	sweepLeftovers(in.opts.RunsDir, in.now())
	return nil
}

func (in *installer) stagingDir() (string, error) {
	if err := in.prepareRuns(); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(in.opts.RunsDir, stagePrefix+in.name+"-*")
	if err != nil {
		return "", fmt.Errorf("install model: create the staging directory: %w", err)
	}
	return staging, nil
}

// place moves staged to the target after one last look at what is there.
func (in *installer) place(ctx context.Context, staged string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("install model: %w", err)
	}
	if _, err := os.Lstat(in.target); err == nil && in.opts.Force {
		if err := in.guard(ctx); err != nil {
			return err
		}
	}
	previous, err := swapIn(os.Rename, staged, in.target, in.opts.Force, in.now())
	if err != nil {
		return err
	}
	if previous != "" {
		_ = os.RemoveAll(previous)
	}
	return nil
}

// swapIn moves staged to target. An existing target is refused unless replace is set; then it is renamed aside, returned, and put back when the move fails.
func swapIn(rename func(oldPath, newPath string) error, staged, target string, replace bool, now time.Time) (previous string, err error) {
	if _, err := os.Lstat(target); err == nil {
		if !replace {
			return "", fmt.Errorf("%w: %s appeared during the install; pass --force to replace it", ErrExists, target)
		}
		previous = filepath.Join(filepath.Dir(target), asideName(filepath.Base(target), now))
		if err := rename(target, previous); err != nil {
			return "", fmt.Errorf("install model: move the previous model aside: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("install model: check the previous model: %w", err)
	}
	if err := rename(staged, target); err != nil {
		err = fmt.Errorf("install model: move the model into place: %w", err)
		if previous != "" {
			if restoreErr := rename(previous, target); restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("restore the previous model from %s: %w", previous, restoreErr))
			}
		}
		return "", err
	}
	return previous, nil
}

func asideName(name string, now time.Time) string {
	return oldPrefix + name + "-" + strconv.FormatInt(now.Unix(), 10) + "-" + rand.Text()
}

// asideTime reads the time that asideName put into an entry name.
func asideTime(entry string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(entry, oldPrefix)
	if !ok {
		return time.Time{}, false
	}
	i := strings.LastIndexByte(rest, '-')
	if i < 0 {
		return time.Time{}, false
	}
	rest = rest[:i]
	secs, err := strconv.ParseInt(rest[strings.LastIndexByte(rest, '-')+1:], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}

// sweepLeftovers removes staging entries untouched for a day, aside entries whose name says they are a day old, and orphaned link records.
func sweepLeftovers(runsDir string, now time.Time) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, oldPrefix):
			if at, ok := asideTime(name); !ok || now.Sub(at) <= staleAfter {
				continue
			}
		case strings.HasPrefix(name, stagePrefix):
			if info, err := entry.Info(); err != nil || time.Since(info.ModTime()) <= staleAfter {
				continue
			}
		case strings.HasPrefix(name, linkRecordPrefix):
			if !orphanLinkRecord(runsDir, name) {
				continue
			}
		default:
			continue
		}
		_ = os.RemoveAll(filepath.Join(runsDir, name))
	}
}

// orphanLinkRecord reports a link record whose link was removed or replaced by something else.
func orphanLinkRecord(runsDir, record string) bool {
	name, ok := strings.CutSuffix(strings.TrimPrefix(record, linkRecordPrefix), ".json")
	if !ok {
		return false
	}
	info, err := os.Lstat(filepath.Join(runsDir, name))
	return errors.Is(err, fs.ErrNotExist) || err == nil && info.Mode()&fs.ModeSymlink == 0
}

// within reports whether path is dir or lies below it, by name or once links are resolved.
func within(dir, path string) bool {
	return lexicallyInside(dir, path) || sameOrInside(dir, path)
}

func lexicallyInside(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && filepath.IsLocal(rel)
}

// sameOrInside reports whether path is dir or lies below it once links are resolved.
func sameOrInside(dir, path string) bool {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realDir, realPath)
	return err == nil && filepath.IsLocal(rel)
}

type sourceFile struct {
	rel   string
	size  int64
	mtime time.Time
}

// walkSource lists the files below dir that an install takes, outside hidden directories and following links as fileInfo does.
func walkSource(dir string, limit int64, link bool) ([]sourceFile, int64, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, 0, fmt.Errorf("install model: open %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()
	var (
		files []sourceFile
		total int64
	)
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case name == ".":
			return nil
		case entry.IsDir() && strings.HasPrefix(entry.Name(), "."):
			return fs.SkipDir
		case entry.IsDir() || skipped(name):
			return nil
		}
		info, err := fileInfo(dir, name, entry, link)
		if err != nil || info == nil {
			return err
		}
		if info.Size() > limit-total || len(files) >= maxSourceFiles {
			return fmt.Errorf("%w: %s holds more than %d bytes or %d files", ErrTooLarge, dir, limit, maxSourceFiles)
		}
		total += info.Size()
		files = append(files, sourceFile{rel: name, size: info.Size(), mtime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("install model: read %s: %w", dir, err)
	}
	return files, total, nil
}

// fileInfo describes a regular file, or a link to one, or returns nil for an entry the install leaves out.
// A copy refuses a link out of dir; a link install records any link by its target.
func fileInfo(dir, name string, entry fs.DirEntry, link bool) (fs.FileInfo, error) {
	switch {
	case entry.Type().IsRegular():
		return entry.Info()
	case entry.Type()&fs.ModeSymlink == 0:
		return nil, nil
	}
	full := filepath.Join(dir, filepath.FromSlash(name))
	if !link && linkOutside(dir, full) {
		return nil, fmt.Errorf("%w: %s is a symbolic link out of the source directory; pass --link, or download with --local-dir", ErrInvalid, name)
	}
	if info, err := os.Stat(full); err == nil && info.Mode().IsRegular() {
		return info, nil
	}
	return nil, nil
}

// stageFile writes body to a new file rel inside root and returns the bytes written, at most limit+1, and their SHA-256.
func stageFile(ctx context.Context, root *os.Root, rel string, body io.Reader, limit int64, report func(done int64)) (int64, string, error) {
	native := filepath.FromSlash(rel)
	if dir := filepath.Dir(native); dir != "." {
		if err := root.MkdirAll(dir, subdirMode); err != nil {
			return 0, "", fmt.Errorf("create the directory: %w", err)
		}
	}
	out, err := root.OpenFile(native, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return 0, "", fmt.Errorf("create the file: %w", err)
	}
	digest := sha256.New()
	counter := &countWriter{ctx: ctx, report: report}
	written, copyErr := io.Copy(io.MultiWriter(out, digest, counter), io.LimitReader(body, limit+1))
	closeErr := out.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return written, "", err
	}
	return written, hex.EncodeToString(digest.Sum(nil)), nil
}

type countWriter struct {
	ctx    context.Context
	done   int64
	report func(done int64)
}

func (c *countWriter) Write(data []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	c.done += int64(len(data))
	if c.report != nil {
		c.report(c.done)
	}
	return len(data), nil
}
