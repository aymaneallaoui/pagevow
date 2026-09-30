package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

const otherEntriesBytes = 16 << 20

func binaryName(goos string) string { return "pagevow" + binarySuffix(goos) }

func extractBinary(archivePath, goos string, dst io.Writer, limit int64) error {
	if goos == windows {
		return extractZipBinary(archivePath, binaryName(goos), dst, limit)
	}
	return extractTarBinary(archivePath, binaryName(goos), dst, limit)
}

func extractTarBinary(archivePath, name string, dst io.Writer, limit int64) error {
	file, err := os.Open(archivePath) //nolint:gosec // the path is a file this package created
	if err != nil {
		return fmt.Errorf("open the archive: %w", err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("read the archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(io.LimitReader(gz, limit+otherEntriesBytes))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the archive has no %s entry", name)
		}
		if err != nil {
			return fmt.Errorf("read the archive: %w", err)
		}
		if strings.TrimPrefix(header.Name, "./") != name {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return fmt.Errorf("the %s entry is not a regular file", name)
		}
		if header.Size > limit {
			return fmt.Errorf("the %s entry is %d bytes, more than the limit of %d", name, header.Size, limit)
		}
		return copyBinary(dst, reader, name, limit)
	}
}

func extractZipBinary(archivePath, name string, dst io.Writer, limit int64) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open the archive: %w", err)
	}
	defer func() { _ = reader.Close() }()
	index := slices.IndexFunc(reader.File, func(entry *zip.File) bool { return entry.Name == name })
	if index < 0 {
		return fmt.Errorf("the archive has no %s entry", name)
	}
	entry := reader.File[index]
	if !entry.Mode().IsRegular() {
		return fmt.Errorf("the %s entry is not a regular file", name)
	}
	if entry.UncompressedSize64 > uint64(limit) { //nolint:gosec // limit is a positive constant or test value
		return fmt.Errorf("the %s entry is %d bytes, more than the limit of %d", name, entry.UncompressedSize64, limit)
	}
	src, err := entry.Open()
	if err != nil {
		return fmt.Errorf("open the %s entry: %w", name, err)
	}
	defer func() { _ = src.Close() }()
	return copyBinary(dst, src, name, limit)
}

func copyBinary(dst io.Writer, src io.Reader, name string, limit int64) error {
	copied, err := io.Copy(dst, io.LimitReader(src, limit+1))
	if err != nil {
		return fmt.Errorf("extract %s: %w", name, err)
	}
	if copied > limit {
		return fmt.Errorf("the %s entry is more than the limit of %d bytes", name, limit)
	}
	if copied == 0 {
		return fmt.Errorf("the %s entry is empty", name)
	}
	return nil
}
