package cli

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/server"
)

const localServingUnsupported = "local model serving is supported on Linux with an NVIDIA GPU and on macOS with Apple Silicon; use backend jev or custom on this system"

// modelLeg is one model server that pagevow starts on this machine.
type modelLeg struct {
	label    string
	url      string
	port     int
	model    string
	mode     string
	modeFlag string
}

func (l modelLeg) peakGiB() float64 {
	peak, _ := server.Peak(l.mode)
	return peak
}

// memory returns the peak memory in GiB of the leg on p and whether it needs the memory floor of a model above 1B on MLX.
func (l modelLeg) memory(p server.Platform, kevDir string) (float64, bool) {
	if !p.MLX() {
		return l.peakGiB(), false
	}
	size, known := server.RunSizeBillions(kevDir, l.model)
	return server.MLXPeak(size, known), server.MLXLarge(size, known)
}

// bf16Fix is the command that switches the leg to mode bf16.
func (l modelLeg) bf16Fix(backend string) string {
	return fmt.Sprintf("pagevow use %s %s bf16", backend, l.modeFlag)
}

func (l modelLeg) recordName() string { return server.RecordName(server.KindModel, l.port) }

func (l modelLeg) readyURL() (string, error) { return server.ModelsURL(l.url) }

// modelLegs lists the model servers the active backend needs on this machine, largest peak first.
func modelLegs(cfg config.Config) ([]modelLeg, error) {
	var candidates []modelLeg
	switch cfg.Backend {
	case config.BackendLocal:
		l := cfg.Backends.Local
		candidates = append(candidates, modelLeg{label: "local model", url: l.URL, model: l.Model, mode: l.Mode, modeFlag: "--mode"})
	case config.BackendCascade:
		c := cfg.Backends.Cascade
		candidates = append(candidates,
			modelLeg{label: "cascade primary", url: c.Primary, model: c.PrimaryModel, mode: c.PrimaryMode, modeFlag: "--primary-mode"},
			modelLeg{label: "cascade verifier", url: c.Verifier, model: c.VerifierModel, mode: c.VerifierMode, modeFlag: "--verifier-mode"})
	}
	var legs []modelLeg
	for _, leg := range candidates {
		if !config.IsLoopbackURL(leg.url) {
			continue
		}
		port, err := portOfURL(leg.url)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", leg.label, err)
		}
		leg.port = port
		if slices.ContainsFunc(legs, func(other modelLeg) bool { return other.port == leg.port }) {
			return nil, fmt.Errorf("%s: port %d is also used by the other model server; give each its own URL", leg.label, leg.port)
		}
		legs = append(legs, leg)
	}
	slices.SortStableFunc(legs, func(a, b modelLeg) int {
		switch {
		case a.peakGiB() > b.peakGiB():
			return -1
		case a.peakGiB() < b.peakGiB():
			return 1
		}
		return 0
	})
	return legs, nil
}

func portOfURL(raw string) (int, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return 0, fmt.Errorf("parse url %q: %w", raw, err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("url %q needs an explicit port, for example http://127.0.0.1:8009", raw)
	}
	return port, nil
}

// localTextHelper describes the llama-server that pagevow starts for the text helper.
type localTextHelper struct {
	url  string
	port int
}

func (t localTextHelper) recordName() string { return server.RecordName(server.KindTextHelper, t.port) }

func (t localTextHelper) readyURL() string {
	parsed, err := url.Parse(t.url)
	if err != nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host + "/v1/models"
}

// localTextHelperOf returns the text helper pagevow starts, or nil when the configuration does not ask for one.
func localTextHelperOf(cfg config.Config) (*localTextHelper, error) {
	if !cfg.TextHelper.Local.Enabled {
		return nil, nil
	}
	switch {
	case cfg.TextHelper.URL == "":
		return nil, errors.New("text_helper.local.enabled is true but text_helper.url is not set; set it to a loopback URL such as http://127.0.0.1:8081/v1")
	case !config.IsLoopbackURL(cfg.TextHelper.URL):
		return nil, fmt.Errorf("text_helper.local.enabled is true but text_helper.url %s is not a loopback address, so pagevow would not reach the server it starts", cfg.TextHelper.URL)
	}
	port, err := portOfURL(cfg.TextHelper.URL)
	if err != nil {
		return nil, fmt.Errorf("text helper: %w", err)
	}
	return &localTextHelper{url: cfg.TextHelper.URL, port: port}, nil
}

func kevDirOf(cfg config.Config, home HomeDir) (string, error) {
	dir, err := config.ExpandHome(cfg.Server.KevDir, home)
	if err != nil {
		return "", fmt.Errorf("server.kev_dir: %w", err)
	}
	return dir, nil
}

func logPathOf(logDir, name string) string { return filepath.Join(logDir, name+".log") }

// platformOf returns the platform the commands assume.
func (a *app) platformOf() (server.Platform, error) {
	goos, err := service[GOOS](a)
	if err != nil {
		return server.Platform{}, err
	}
	goarch, err := service[GOARCH](a)
	if err != nil {
		return server.Platform{}, err
	}
	return server.Platform{OS: string(goos), Arch: string(goarch)}, nil
}

func guardOf(cfg config.Config) server.Guard {
	return server.Guard{Enabled: cfg.Server.GPUWatch, MaxTempC: cfg.Server.GPUMaxTempC, MinFreeMiB: cfg.Server.GPUMinFreeMiB}
}
