package cli

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/server"
	"github.com/aymaneallaoui/pagevow/internal/ui"
)

const (
	resultStopped      = "stopped"
	resultStaleRemoved = "stale record removed"
	resultFailed       = "failed"
)

type stoppedProcess struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	PID    int    `json:"pid"`
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

type stopReport struct {
	OK       bool             `json:"ok"`
	Stopped  []stoppedProcess `json:"stopped"`
	Problems []string         `json:"problems"`
}

func (a *app) newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop everything pagevow started",
		Long: "Stop the browser, the text helper and the model servers that pagevow start recorded, in that order, and remove stale records.\n" +
			"A process is only signalled when its recorded identity still matches. The managed browser profile stays on disk.\n\n" +
			"Exit codes: 0 everything is stopped, 2 a process could not be stopped.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.runStop(cmd) },
	}
	cmd.Flags().Bool("json", false, "print the result as JSON")
	return cmd
}

func (a *app) runStop(cmd *cobra.Command) error {
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return fmt.Errorf("read --json: %w", err)
	}
	procs, err := service[Processes](a)
	if err != nil {
		return err
	}
	ctx := commandContext(cmd)
	records, listErr := procs.List()
	slices.SortStableFunc(records, func(x, y server.Record) int { return stopRank(x.Kind) - stopRank(y.Kind) })

	report := stopReport{OK: true, Stopped: []stoppedProcess{}, Problems: []string{}}
	var out *ui.Printer
	if !asJSON {
		if out, err = a.printer(cmd.OutOrStdout()); err != nil {
			return err
		}
	}
	if listErr != nil {
		report.OK = false
		report.Problems = append(report.Problems, fmt.Sprintf("some process records could not be read: %v", listErr))
		if out != nil {
			out.Status(ui.Fail, "%s", report.Problems[0])
		}
	}
	for i, rec := range records {
		entry := stoppedProcess{Name: rec.Name, Kind: string(rec.Kind), PID: rec.PID}
		result, err := procs.Stop(ctx, rec)
		switch {
		case err != nil:
			entry.Result, entry.Error = resultFailed, err.Error()
			report.OK = false
		case result == server.WasStale:
			entry.Result = resultStaleRemoved
		default:
			entry.Result = resultStopped
		}
		report.Stopped = append(report.Stopped, entry)
		if out != nil {
			printStopped(out, entry)
		}
		if ctx.Err() != nil && i < len(records)-1 {
			report.OK = false
			problem := fmt.Sprintf("interrupted: %d record(s) were not processed", len(records)-1-i)
			report.Problems = append(report.Problems, problem)
			if out != nil {
				out.Status(ui.Fail, "%s", problem)
			}
			break
		}
	}
	if out != nil {
		if len(records) == 0 && listErr == nil {
			out.Status(ui.OK, "nothing to stop: pagevow has no recorded processes")
		}
		if err := out.Err(); err != nil {
			return err
		}
	}
	if asJSON {
		if err := writeJSON(cmd, report); err != nil {
			return err
		}
	}
	if !report.OK {
		return infrastructure(errors.New("not everything could be stopped"))
	}
	return nil
}

func stopRank(kind server.Kind) int {
	switch kind {
	case server.KindBrowser:
		return 0
	case server.KindTextHelper:
		return 1
	}
	return 2
}

func printStopped(out *ui.Printer, entry stoppedProcess) {
	switch entry.Result {
	case resultStopped:
		out.Status(ui.OK, "%s stopped (pid %d)", entry.Name, entry.PID)
	case resultStaleRemoved:
		out.Status(ui.Info, "%s: its process was already gone, record removed", entry.Name)
	default:
		out.Status(ui.Fail, "%s: %s", entry.Name, entry.Error)
	}
}
