// Package model installs a local model run directory from a path or a Hugging Face repository into the kev runs directory.
package model

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxNameLen     = 128
	maxSourceLen   = 512
	maxRevisionLen = 255
)

// ErrInvalidSource reports an argument that is neither a directory nor a repository name.
var ErrInvalidSource = errors.New("invalid model source")

// ErrInvalidName reports a model name that cannot be a directory name under the runs directory.
var ErrInvalidName = errors.New("invalid model name")

var (
	repoPartPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)
	revisionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+-]*$`)
	namePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// Source says where a model comes from: exactly one of Path and Repo is set.
type Source struct {
	Path     string
	Repo     string
	Revision string
}

// String returns the path, or the repository with its revision when one was given.
func (s Source) String() string {
	switch {
	case s.Path != "":
		return s.Path
	case s.Revision != "":
		return s.Repo + "@" + s.Revision
	}
	return s.Repo
}

func (s Source) defaultName() string {
	if s.Path != "" {
		return filepath.Base(s.Path)
	}
	_, name, _ := strings.Cut(s.Repo, "/")
	return name
}

func (s Source) check(link bool) error {
	switch {
	case (s.Path == "") == (s.Repo == ""):
		return fmt.Errorf("%w: give either a path or a repository", ErrInvalidSource)
	case link && s.Path == "":
		return fmt.Errorf("%w: a link needs a directory on this machine", ErrInvalidSource)
	}
	return nil
}

// ParseSource reads the argument of install --model: an existing directory, or OWNER/NAME with an optional @REVISION.
func ParseSource(arg string) (Source, error) {
	if arg == "" || len(arg) > maxSourceLen {
		return Source{}, fmt.Errorf("%w: expected a directory or OWNER/NAME[@REVISION]", ErrInvalidSource)
	}
	info, err := os.Stat(arg)
	switch {
	case err == nil && !info.IsDir():
		return Source{}, fmt.Errorf("%w: %s is not a directory", ErrInvalidSource, arg)
	case err == nil:
		abs, err := filepath.Abs(arg)
		if err != nil {
			return Source{}, fmt.Errorf("resolve %s: %w", arg, err)
		}
		return Source{Path: abs}, nil
	case !errors.Is(err, fs.ErrNotExist):
		return Source{}, fmt.Errorf("read %s: %w", arg, err)
	}
	repo, revision, hasRevision := strings.Cut(arg, "@")
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || !repoPart(owner) || !repoPart(name) || hasRevision && !validRevision(revision) {
		return Source{}, fmt.Errorf("%w: %q is not an existing directory and not OWNER/NAME[@REVISION]", ErrInvalidSource, arg)
	}
	return Source{Repo: repo, Revision: revision}, nil
}

func repoPart(part string) bool {
	return repoPartPattern.MatchString(part) && !strings.Contains(part, "..")
}

func validRevision(revision string) bool {
	return len(revision) <= maxRevisionLen && revisionPattern.MatchString(revision) &&
		!strings.Contains(revision, "..") && !strings.Contains(revision, "//") && !strings.HasSuffix(revision, "/")
}

func checkName(name string) error {
	if len(name) > maxNameLen || !namePattern.MatchString(name) || strings.HasSuffix(name, ".") {
		return fmt.Errorf("%w %q: use letters, digits, dots, dashes and underscores, starting with a letter or digit; pass --name to choose one", ErrInvalidName, name)
	}
	return nil
}
