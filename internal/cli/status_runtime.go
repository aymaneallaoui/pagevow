package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/runner"
	"github.com/aymaneallaoui/pagevow/internal/server"
	"github.com/aymaneallaoui/pagevow/internal/ui"
	"github.com/aymaneallaoui/pagevow/internal/version"
)

type healthTarget struct {
	name      string
	label     string
	url       string
	probeURL  string
	startable bool
}

// healthTargets lists the destinations a run would use: the active backend and the text helper.
func healthTargets(cfg config.Config) []healthTarget {
	var targets []healthTarget
	model := func(name, label, url string, startable bool) {
		probe, err := server.ModelsURL(url)
		if err != nil {
			probe = url
		}
		targets = append(targets, healthTarget{name: name, label: label, url: url, probeURL: probe, startable: startable && config.IsLoopbackURL(url)})
	}
	switch cfg.Backend {
	case config.BackendLocal:
		model("local", "local model server", cfg.Backends.Local.URL, true)
	case config.BackendJev:
		model("jev", "jev decision backend", cfg.Backends.Jev.URL, false)
	case config.BackendCustom:
		model("custom", "custom decision backend", cfg.Backends.Custom.URL, false)
	case config.BackendCascade:
		model("cascade_primary", "cascade primary model", cfg.Backends.Cascade.Primary, true)
		model("cascade_verifier", "cascade verifier model", cfg.Backends.Cascade.Verifier, true)
	}
	if cfg.TextHelper.URL != "" {
		targets = append(targets, healthTarget{
			name: "text_helper", label: "text helper", url: cfg.TextHelper.URL,
			probeURL: strings.TrimRight(cfg.TextHelper.URL, "/") + "/models", startable: cfg.TextHelper.Local.Enabled && config.IsLoopbackURL(cfg.TextHelper.URL),
		})
	}
	return targets
}

func (a *app) collectRuntime(ctx context.Context, cfg config.Config, report *statusReport) error {
	procs, err := service[Processes](a)
	if err != nil {
		return err
	}
	gpu, err := service[GPUReader](a)
	if err != nil {
		return err
	}
	browsers, err := service[ManagedBrowsers](a)
	if err != nil {
		return err
	}
	now, err := service[Clock](a)
	if err != nil {
		return err
	}
	info, err := service[version.Info](a)
	if err != nil {
		return err
	}
	report.Versions = map[string]string{"pagevow": info.Version}
	report.Processes, report.StaleRemoved = []processState{}, []string{}
	report.Health, report.Tripped = []healthState{}, []trippedState{}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		report.Health = probeHealth(ctx, procs, healthTargets(cfg))
	}()
	go func() {
		defer wg.Done()
		if reading, err := gpu.Read(ctx); err == nil {
			report.GPU = gpuStateOf(reading)
		}
	}()
	swept := sweepRecords(ctx, procs)
	report.Processes = processStates(ctx, procs, swept, now())
	for _, rec := range swept.Gone {
		report.StaleRemoved = append(report.StaleRemoved, rec.Name)
	}
	for _, p := range report.Processes {
		if p.Kind != string(server.KindBrowser) || !p.Alive {
			continue
		}
		if v, err := browsers.Version(ctx, browser.DebugURL(p.Port)); err == nil {
			report.Versions["browser"] = v
		}
	}
	if found, err := procs.Tripped(); err == nil {
		for _, t := range found {
			report.Tripped = append(report.Tripped, trippedState{Name: t.Name, Message: t.Message})
		}
	}
	wg.Wait()
	return nil
}

const (
	stateReady    = "ready"
	stateStarting = "starting"
	stateOrphaned = "orphaned"
	stateGone     = "gone"
)

func processStates(ctx context.Context, procs Processes, swept sweep, now time.Time) []processState {
	running := slices.Concat(swept.Live, swept.Orphaned)
	states := make([]processState, 0, len(running)+len(swept.Gone))
	for i, rec := range running {
		state := processState{
			Name: rec.Name, Kind: string(rec.Kind), PID: rec.PID, ChildPID: rec.ChildPID, Port: rec.Port, Log: rec.Log,
			Alive: i < len(swept.Live), UptimeSeconds: max(int64(now.Sub(rec.StartedAt).Seconds()), 0),
		}
		if !state.Alive {
			state.State = stateOrphaned
		}
		states = append(states, state)
	}
	var wg sync.WaitGroup
	for i, rec := range running {
		wg.Add(1)
		go func() {
			defer wg.Done()
			states[i].Ready = answered(procs.Probe(ctx, readyURLOf(rec)))
			if states[i].State == "" {
				states[i].State = stateStarting
				if states[i].Ready {
					states[i].State = stateReady
				}
			}
		}()
	}
	wg.Wait()
	for _, rec := range swept.Gone {
		states = append(states, processState{Name: rec.Name, Kind: string(rec.Kind), PID: rec.PID, ChildPID: rec.ChildPID, Port: rec.Port, State: stateGone, Log: rec.Log})
	}
	slices.SortFunc(states, func(a, b processState) int { return strings.Compare(a.Name, b.Name) })
	return states
}

func readyURLOf(rec server.Record) string {
	if rec.Kind == server.KindBrowser {
		return browser.DebugURL(rec.Port) + "/json/version"
	}
	return rec.ReadyURL
}

func probeHealth(ctx context.Context, procs Processes, targets []healthTarget) []healthState {
	states := make([]healthState, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state := healthState{Name: target.name, URL: target.url}
			status, err := procs.Probe(ctx, target.probeURL)
			switch {
			case err != nil:
				state.Error = runner.RootCause(err)
			case !answered(status, nil):
				state.HTTPStatus, state.Error = status, fmt.Sprintf("HTTP %d", status)
			default:
				state.Reachable, state.HTTPStatus = true, status
			}
			states[i] = state
		}()
	}
	wg.Wait()
	return states
}

func renderRuntime(out *ui.Printer, report statusReport, platform server.Platform) {
	out.Blank()
	out.Heading("Processes")
	if len(report.Processes) == 0 {
		out.Line("none recorded")
	} else {
		rows := make([][]string, 0, len(report.Processes))
		for _, p := range report.Processes {
			rows = append(rows, []string{p.Name, fmt.Sprint(p.PID), fmt.Sprint(p.Port), p.State, uptimeText(p), p.Log})
		}
		out.Table([]string{"NAME", "PID", "PORT", "STATE", "UPTIME", "LOG"}, rows)
	}
	for _, p := range report.Processes {
		if p.State == stateOrphaned {
			out.Status(ui.Warn, "%s; fix: pagevow stop", orphanText(p.Name, p.PID, p.ChildPID))
		}
	}
	for _, name := range report.StaleRemoved {
		out.Status(ui.Info, "removed stale record %s: its process is gone", name)
	}
	out.Blank()
	out.Heading("Health")
	if len(report.Health) == 0 {
		out.Line("nothing to check")
	}
	for _, h := range report.Health {
		if h.Reachable {
			out.Status(ui.OK, "%s at %s answers (HTTP %d)", h.Name, h.URL, h.HTTPStatus)
		} else {
			out.Status(ui.Warn, "%s at %s does not answer: %s", h.Name, h.URL, h.Error)
		}
	}
	out.Blank()
	renderMemory(out, report.GPU, platform)
	out.Blank()
	out.Heading("Versions")
	versions := []ui.Pair{{Key: "pagevow", Value: report.Versions["pagevow"]}}
	if v, ok := report.Versions["browser"]; ok {
		versions = append(versions, ui.Pair{Key: "browser", Value: v})
	}
	out.Pairs(versions)
	if len(report.Tripped) > 0 {
		out.Blank()
		out.Heading("Stopped by the GPU guard")
		for _, t := range report.Tripped {
			out.Status(ui.Warn, "%s", t.Message)
		}
	}
}

func gpuStateOf(reading server.GPU) *gpuState {
	state := &gpuState{TotalMiB: reading.TotalMiB, UsedMiB: reading.UsedMiB, FreeMiB: reading.FreeMiB, TemperatureC: reading.TempC, Unified: reading.Unified}
	if reading.Unified {
		parts := reading.Parts
		state.Components = &memoryComponents{
			FreeMiB: parts.FreeMiB, SpeculativeMiB: parts.SpeculativeMiB, PurgeableMiB: parts.PurgeableMiB, FileBackedMiB: parts.FileBackedMiB,
		}
	}
	return state
}

func renderMemory(out *ui.Printer, gpu *gpuState, platform server.Platform) {
	switch {
	case gpu == nil && platform.MLX():
		out.Heading("Memory")
		out.Line("unknown (sysctl could not be read)")
	case gpu == nil:
		out.Heading("GPU")
		out.Line("unknown (nvidia-smi is not available)")
	case gpu.Unified:
		out.Heading("Memory")
		pairs := []ui.Pair{{Key: "unified", Value: fmt.Sprintf("%d MiB free, %d MiB used, %d MiB total", gpu.FreeMiB, gpu.UsedMiB, gpu.TotalMiB)}}
		if c := gpu.Components; c != nil {
			pairs = append(pairs, ui.Pair{Key: "counted free", Value: fmt.Sprintf("%d MiB free + %d purgeable + %d file-backed (%d speculative, inside file-backed)", c.FreeMiB, c.PurgeableMiB, c.FileBackedMiB, c.SpeculativeMiB)})
		}
		if gpu.TemperatureC != 0 {
			pairs = append(pairs, ui.Pair{Key: "temperature", Value: fmt.Sprintf("%d C", gpu.TemperatureC)})
		}
		out.Pairs(pairs)
	default:
		out.Heading("GPU")
		out.Pairs([]ui.Pair{
			{Key: "memory", Value: fmt.Sprintf("%d MiB free, %d MiB used, %d MiB total", gpu.FreeMiB, gpu.UsedMiB, gpu.TotalMiB)},
			{Key: "temperature", Value: fmt.Sprintf("%d C", gpu.TemperatureC)},
		})
	}
}

func uptimeText(p processState) string {
	if p.State == stateGone {
		return "-"
	}
	return (time.Duration(p.UptimeSeconds) * time.Second).String()
}
