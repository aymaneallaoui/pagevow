// Package plugin renders, installs and registers the Claude Code plugin that ships inside the pagevow binary.
package plugin

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Identity of the plugin and of the local marketplace that wraps it.
const (
	Name        = "pagevow"
	Marketplace = "pagevow"
	ID          = "pagevow@pagevow"
	RootDirName = "claude-plugin"
)

const (
	pluginSubdir      = "plugin"
	manifestRel       = ".claude-plugin/plugin.json"
	hooksRel          = "hooks/hooks.json"
	marketplaceRel    = ".claude-plugin/marketplace.json"
	defaultVersion    = "0.0.0"
	versionMarker     = "{{VERSION}}"
	hookCommandMarker = "{{HOOK_COMMAND}}"
	maxManifestBytes  = 64 << 10
	maxOutputBytes    = 1024
)

var (
	// ErrForeignRoot means the target directory exists but is not a pagevow plugin tree.
	ErrForeignRoot = errors.New("directory exists and is not a pagevow plugin")
	// ErrSymlinkRoot means the target directory is a symbolic link, which pagevow never follows.
	ErrSymlinkRoot = errors.New("directory is a symbolic link")
)

//go:embed all:files
var embedded embed.FS

// Runner runs an external command and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Options describes one rendering of the plugin.
type Options struct {
	Root        string
	Version     string
	HookCommand string
}

// Files returns the rendered plugin tree as path -> content, paths relative to Options.Root and slash separated.
func Files(opts Options) (map[string][]byte, error) {
	if strings.TrimSpace(opts.HookCommand) == "" {
		return nil, errors.New("render plugin: the hook command is empty")
	}
	version := opts.Version
	if version == "" {
		version = defaultVersion
	}
	tokens := map[string][2]string{
		manifestRel: {versionMarker, version},
		hooksRel:    {hookCommandMarker, opts.HookCommand},
	}

	out := make(map[string][]byte)
	err := fs.WalkDir(embedded, "files", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := embedded.ReadFile(p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, "files/")
		if token, ok := tokens[rel]; ok {
			escaped, err := jsonEscape(token[1])
			if err != nil {
				return fmt.Errorf("escape value for %s: %w", rel, err)
			}
			data = bytes.ReplaceAll(data, []byte(token[0]), []byte(escaped))
		}
		if bytes.Contains(data, []byte("{{")) {
			return fmt.Errorf("%s has an unresolved template token", rel)
		}
		out[path.Join(pluginSubdir, rel)] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("render plugin: %w", err)
	}

	market, err := marketplaceJSON()
	if err != nil {
		return nil, fmt.Errorf("render plugin: %w", err)
	}
	out[marketplaceRel] = market
	return out, nil
}

func jsonEscape(value string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return "", err
	}
	quoted := strings.TrimSuffix(buf.String(), "\n")
	return quoted[1 : len(quoted)-1], nil
}

func marketplaceJSON() ([]byte, error) {
	type owner struct {
		Name string `json:"name"`
	}
	type entry struct {
		Name        string `json:"name"`
		Source      string `json:"source"`
		Description string `json:"description"`
	}
	doc := struct {
		Name        string  `json:"name"`
		Owner       owner   `json:"owner"`
		Description string  `json:"description"`
		Plugins     []entry `json:"plugins"`
	}{
		Name:        Marketplace,
		Owner:       owner{Name: Name},
		Description: "Local marketplace for the pagevow plugin.",
		Plugins: []entry{{
			Name:        Name,
			Source:      "./" + pluginSubdir,
			Description: "Browser tests written as goals, run by pagevow.",
		}},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Install writes the plugin tree under opts.Root; it reports whether anything changed.
func Install(ctx context.Context, opts Options) (changed bool, err error) {
	if opts.Root == "" {
		return false, errors.New("install plugin: the root directory is empty")
	}
	files, err := Files(opts)
	if err != nil {
		return false, err
	}
	root := filepath.Clean(opts.Root)
	exists, err := inspectRoot(root)
	if err != nil {
		return false, fmt.Errorf("install plugin into %s: %w", root, err)
	}
	if exists && sameTree(root, files) {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("install plugin into %s: %w", root, err)
	}
	if err := swapTree(root, files); err != nil {
		return false, fmt.Errorf("install plugin into %s: %w", root, err)
	}
	return true, nil
}

func inspectRoot(root string) (exists bool, err error) {
	info, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return true, ErrSymlinkRoot
	}
	if !info.IsDir() {
		return true, fmt.Errorf("%w: not a directory", ErrForeignRoot)
	}
	if Installed(root) {
		return true, nil
	}
	empty, err := isEmptyDir(root)
	if err != nil {
		return true, err
	}
	if empty {
		return false, nil
	}
	return true, ErrForeignRoot
}

func isEmptyDir(dir string) (bool, error) {
	f, err := os.Open(dir) //nolint:gosec // dir was just checked with Lstat
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Readdirnames(1); errors.Is(err, io.EOF) {
		return true, nil
	} else if err != nil {
		return false, err
	}
	return false, nil
}

func sameTree(root string, want map[string][]byte) bool {
	seen := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return errors.New("not a regular file")
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		expected, ok := want[filepath.ToSlash(rel)]
		if !ok {
			return errors.New("unexpected file")
		}
		got, err := os.ReadFile(p) //nolint:gosec // p comes from walking the plugin root
		if err != nil {
			return err
		}
		if !bytes.Equal(got, expected) {
			return errors.New("content differs")
		}
		seen++
		return nil
	})
	return err == nil && seen == len(want)
}

func swapTree(root string, files map[string][]byte) error {
	if err := os.MkdirAll(filepath.Dir(root), 0o750); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	suffix := "-" + strconv.Itoa(os.Getpid())
	tmp := root + ".tmp" + suffix
	old := root + ".old" + suffix
	for _, stale := range []string{tmp, old} {
		if err := os.RemoveAll(stale); err != nil {
			return fmt.Errorf("clear %s: %w", stale, err)
		}
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := writeTree(tmp, files); err != nil {
		return err
	}
	_, statErr := os.Lstat(root)
	hadRoot := statErr == nil
	if hadRoot {
		if err := os.Rename(root, old); err != nil {
			return fmt.Errorf("move the old tree aside: %w", err)
		}
	}
	if err := os.Rename(tmp, root); err != nil {
		if hadRoot {
			_ = os.Rename(old, root)
		}
		return fmt.Errorf("move the new tree into place: %w", err)
	}
	if hadRoot {
		if err := os.RemoveAll(old); err != nil {
			return fmt.Errorf("remove the old tree: %w", err)
		}
	}
	return nil
}

func writeTree(dir string, files map[string][]byte) error {
	names := make([]string, 0, len(files))
	for name := range files {
		if !fs.ValidPath(name) {
			return fmt.Errorf("invalid plugin file path %q", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	for _, name := range names {
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return fmt.Errorf("create directory for %s: %w", name, err)
		}
		if err := os.WriteFile(target, files[name], 0o600); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

// Register runs the claude CLI to add the marketplace, then install the plugin or update it when already installed.
func Register(ctx context.Context, run Runner, claude string, root string) error {
	listed, err := run.Run(ctx, claude, "plugin", "marketplace", "list")
	alreadyAdded := err == nil && bytes.Contains(listed, []byte(Marketplace))
	if !alreadyAdded {
		if err := runClaude(ctx, run, claude, "plugin", "marketplace", "add", root); err != nil {
			return err
		}
	}
	if pluginListed(ctx, run, claude) {
		return runClaude(ctx, run, claude, "plugin", "update", ID)
	}
	return runClaude(ctx, run, claude, "plugin", "install", ID, "--scope", "user")
}

func pluginListed(ctx context.Context, run Runner, claude string) bool {
	out, err := run.Run(ctx, claude, "plugin", "list", "--json")
	if err != nil {
		return false
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return false
	}
	for _, e := range entries {
		if e.ID == ID {
			return true
		}
	}
	return false
}

// Unregister runs the claude CLI to uninstall the plugin and remove the marketplace; each failure is returned as a warning.
func Unregister(ctx context.Context, run Runner, claude string) []error {
	var warnings []error
	if err := runClaude(ctx, run, claude, "plugin", "uninstall", ID); err != nil {
		warnings = append(warnings, err)
	}
	if err := runClaude(ctx, run, claude, "plugin", "marketplace", "remove", Marketplace); err != nil {
		warnings = append(warnings, err)
	}
	return warnings
}

func runClaude(ctx context.Context, run Runner, claude string, args ...string) error {
	out, err := run.Run(ctx, claude, args...)
	if err == nil {
		return nil
	}
	label := "claude " + strings.Join(args[:min(len(args), 3)], " ")
	if text := trimOutput(out); text != "" {
		return fmt.Errorf("%s: %w: %s", label, err, text)
	}
	return fmt.Errorf("%s: %w", label, err)
}

func trimOutput(out []byte) string {
	text := strings.TrimSpace(string(out))
	if len(text) > maxOutputBytes {
		text = strings.ToValidUTF8(text[:maxOutputBytes], "") + "..."
	}
	return text
}

// Remove deletes the tree under root when it carries pagevow's manifest; it reports whether anything was removed.
func Remove(root string) (removed bool, err error) {
	if root == "" {
		return false, errors.New("remove plugin: the root directory is empty")
	}
	root = filepath.Clean(root)
	exists, err := inspectRoot(root)
	if err != nil {
		return false, fmt.Errorf("remove plugin from %s: %w", root, err)
	}
	if !exists {
		return false, nil
	}
	if err := os.RemoveAll(root); err != nil {
		return false, fmt.Errorf("remove plugin from %s: %w", root, err)
	}
	return true, nil
}

// Installed reports whether the plugin manifest exists under root.
func Installed(root string) bool {
	f, err := os.Open(filepath.Join(root, pluginSubdir, filepath.FromSlash(manifestRel))) //nolint:gosec // fixed manifest path under the plugin root
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxManifestBytes))
	if err != nil {
		return false
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false
	}
	return manifest.Name == Name
}

// PluginDir returns the directory of the plugin inside the marketplace root.
func PluginDir(root string) string { //nolint:revive // name fixed by the phase 4 plan
	return filepath.Join(root, pluginSubdir)
}

// ShellQuote quotes a path for the hooks.json command on POSIX and Windows shells.
func ShellQuote(p string) string { return shellQuote(p, runtime.GOOS == "windows") }

func shellQuote(p string, windows bool) string {
	if windows {
		return `"` + strings.ReplaceAll(p, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}
