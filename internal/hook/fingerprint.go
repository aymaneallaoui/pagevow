package hook

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var skippedDirs = map[string]bool{
	OutDirName:     true,
	".git":         true,
	"node_modules": true,
	".venv":        true,
	"__pycache__":  true,
}

// Fingerprint summarises the project state: git HEAD, tracked, staged and untracked changes plus the tests file,
// or the newest modification time when dir is not a git work tree or git is nil.
func Fingerprint(ctx context.Context, git Git, dir, testsFile string) (string, error) {
	if git != nil && insideWorkTree(ctx, git, dir) {
		return gitFingerprint(ctx, git, dir, testsFile)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("fingerprint project: %w", err)
	}
	return mtimeFingerprint(ctx, dir, testsFile)
}

func insideWorkTree(ctx context.Context, git Git, dir string) bool {
	out, err := git.Output(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func gitFingerprint(ctx context.Context, git Git, dir, testsFile string) (string, error) {
	pathspec := []string{"--", ".", ":(exclude)" + OutDirName}
	h := sha256.New()
	head, err := git.Output(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("git rev-parse HEAD: %w", ctxErr)
		}
		head = []byte("no-head\n")
	}
	_, _ = h.Write(head)
	for _, args := range [][]string{
		append([]string{"status", "--porcelain"}, pathspec...),
		append([]string{"diff"}, pathspec...),
		append([]string{"diff", "--cached"}, pathspec...),
	} {
		out, err := git.Output(ctx, dir, args...)
		if err != nil {
			return "", fmt.Errorf("git %s: %w", args[0], err)
		}
		_, _ = h.Write(out)
	}
	untracked, err := git.Output(ctx, dir, append([]string{"ls-files", "--others", "--exclude-standard", "-z"}, pathspec...)...)
	if err != nil {
		return "", fmt.Errorf("git ls-files: %w", err)
	}
	hashUntracked(h, dir, untracked)
	tests, err := os.ReadFile(testsFile) //nolint:gosec // the path is the project's tests file
	if err != nil {
		return "", fmt.Errorf("read tests file: %w", err)
	}
	_, _ = h.Write(tests)
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func hashUntracked(h hash.Hash, dir string, listing []byte) {
	var names []string
	for name := range strings.SplitSeq(string(listing), "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		if line, ok := untrackedLine(dir, name); ok {
			_, _ = io.WriteString(h, line)
		}
	}
}

func untrackedLine(dir, name string) (string, bool) {
	path := filepath.Join(dir, filepath.FromSlash(name))
	info, err := os.Lstat(path)
	if err != nil {
		return "", false
	}
	sum := sha256.New()
	var kind string
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return "", false
		}
		kind = "link"
		_, _ = io.WriteString(sum, target)
	case info.Mode().IsRegular():
		kind = "file"
		if err := copyFile(sum, path); err != nil {
			return "", false
		}
	default:
		return "", false
	}
	return fmt.Sprintf("%s %s %x\n", name, kind, sum.Sum(nil)), true
}

func copyFile(dst io.Writer, path string) (err error) {
	f, err := os.Open(path) //nolint:gosec // the path comes from git ls-files inside the project
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if _, err := io.Copy(dst, f); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

func mtimeFingerprint(ctx context.Context, dir, testsFile string) (string, error) {
	info, err := os.Stat(testsFile)
	if err != nil {
		return "", fmt.Errorf("stat tests file: %w", err)
	}
	newest := info.ModTime().UnixNano()
	walk := func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if entry, err := d.Info(); err == nil {
			newest = max(newest, entry.ModTime().UnixNano())
		}
		return nil
	}
	if err := filepath.WalkDir(dir, walk); err != nil {
		return "", fmt.Errorf("walk project: %w", err)
	}
	return fmt.Sprintf("mtime:%d", newest), nil
}
