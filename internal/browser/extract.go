package browser

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxExtractBytes   int64 = 2 << 30
	maxExtractEntries       = 50000
	dirMode                 = 0o755
	plainFileMode           = 0o644
	execFileMode            = 0o755
)

type extractor struct {
	root *os.Root
	dirs map[string]struct{}
}

// extractZip unpacks reader into dest, which must exist, and refuses anything that could write outside it.
func extractZip(ctx context.Context, reader *zip.Reader, dest string, maxBytes int64) error {
	if len(reader.File) > maxExtractEntries {
		return fmt.Errorf("archive has %d entries, more than the limit of %d", len(reader.File), maxExtractEntries)
	}
	limit := uint64(max(maxBytes, 0))
	var total uint64
	for _, entry := range reader.File {
		if entry.UncompressedSize64 > limit {
			return fmt.Errorf("archive expands to more than %d bytes", maxBytes)
		}
		if total += entry.UncompressedSize64; total > limit {
			return fmt.Errorf("archive expands to more than %d bytes", maxBytes)
		}
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return fmt.Errorf("open extraction directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	ex := extractor{root: root, dirs: map[string]struct{}{}}
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("extract archive: %w", err)
		}
		if err := ex.extract(entry); err != nil {
			return fmt.Errorf("extract %q: %w", entry.Name, err)
		}
	}
	return nil
}

func (e extractor) extract(entry *zip.File) error {
	rel, err := entryPath(entry.Name)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	if err := e.requirePlainParents(rel); err != nil {
		return err
	}
	mode := entry.Mode()
	switch {
	case mode.IsDir():
		if mode.Type() != fs.ModeDir {
			return errors.New("the entry name ends with a slash but the entry is not a directory")
		}
		return e.root.MkdirAll(rel, dirMode)
	case mode&fs.ModeSymlink != 0:
		return e.symlink(entry, rel)
	case mode.IsRegular():
		return e.file(entry, rel, mode)
	}
	return fmt.Errorf("unsupported entry type %s", mode.Type())
}

func entryPath(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\\\x00") {
		return "", errors.New("invalid entry name")
	}
	trimmed := strings.TrimSuffix(name, "/")
	native := filepath.FromSlash(trimmed)
	if native == "" || !filepath.IsLocal(native) {
		return "", errors.New("the entry name leaves the extraction directory")
	}
	if path.Clean(trimmed) != trimmed {
		return "", errors.New("the entry name is not in canonical form")
	}
	return native, nil
}

// requirePlainParents rejects an entry when any existing parent directory is a symbolic link, so case folding and
// unclean names cannot steer a write through a link.
func (e extractor) requirePlainParents(rel string) error {
	parts := strings.Split(rel, string(filepath.Separator))
	for i := 1; i < len(parts); i++ {
		parent := strings.Join(parts[:i], string(filepath.Separator))
		if _, ok := e.dirs[parent]; ok {
			continue
		}
		info, err := e.root.Lstat(parent)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return err
		case info.Mode()&fs.ModeSymlink != 0:
			return errors.New("the entry sits below a symbolic link")
		case !info.IsDir():
			return errors.New("the entry sits below a file")
		}
		e.dirs[parent] = struct{}{}
	}
	return nil
}

func (e extractor) file(entry *zip.File, rel string, mode fs.FileMode) error {
	if err := e.root.MkdirAll(filepath.Dir(rel), dirMode); err != nil {
		return err
	}
	perm := fs.FileMode(plainFileMode)
	if mode&0o111 != 0 {
		perm = execFileMode
	}
	out, err := e.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if err := copyEntry(out, entry); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Chmod(perm); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func copyEntry(dst io.Writer, entry *zip.File) error {
	src, err := entry.Open()
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	if _, err := io.CopyN(dst, src, int64(entry.UncompressedSize64)); err != nil { //nolint:gosec // extractZip rejected sizes above its limit
		return err
	}
	var extra [1]byte
	n, err := src.Read(extra[:])
	switch {
	case n > 0:
		return errors.New("the entry holds more data than it declares")
	case err != nil && !errors.Is(err, io.EOF):
		return err
	}
	return nil
}

func (e extractor) symlink(entry *zip.File, rel string) error {
	src, err := entry.Open()
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	data, err := io.ReadAll(io.LimitReader(src, 4097))
	if err != nil {
		return err
	}
	target := string(data)
	if err := checkLinkTarget(rel, target); err != nil {
		return err
	}
	if err := e.root.MkdirAll(filepath.Dir(rel), dirMode); err != nil {
		return err
	}
	return e.root.Symlink(filepath.FromSlash(target), rel)
}

func checkLinkTarget(rel, target string) error {
	if target == "" || len(target) > 4096 || strings.ContainsAny(target, "\\\x00") {
		return errors.New("invalid symbolic link target")
	}
	if path.IsAbs(target) {
		return errors.New("the symbolic link target is absolute")
	}
	seenName := false
	for _, part := range strings.Split(target, "/") {
		switch part {
		case "", ".":
		case "..":
			if seenName {
				return errors.New("the symbolic link target climbs after descending")
			}
		default:
			seenName = true
		}
	}
	resolved := filepath.Join(filepath.Dir(rel), filepath.FromSlash(target))
	if !filepath.IsLocal(resolved) {
		return errors.New("the symbolic link target leaves the extraction directory")
	}
	return nil
}
