//go:build !windows

package server_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func ignoreTermScript(t *testing.T) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "trap-installed")
	return `trap "" TERM; : > ` + marker + `; while :; do sleep 0.1; done`
}

func waitForMarker(t *testing.T, script string) {
	t.Helper()
	marker := script[len(`trap "" TERM; : > `):]
	marker = marker[:len(marker)-len(`; while :; do sleep 0.1; done`)]
	require.Eventually(t, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond, "the helper never installed its trap")
}

func newStore(t *testing.T) *server.Store {
	t.Helper()
	return server.NewStore(filepath.Join(t.TempDir(), "run"))
}

func modelSpec(t *testing.T, port int, argv ...string) server.Spec {
	t.Helper()
	return server.Spec{
		Name:        server.RecordName(server.KindModel, port),
		Kind:        server.KindModel,
		Argv:        argv,
		Dir:         t.TempDir(),
		Port:        port,
		ReadyURL:    "http://127.0.0.1:" + strconv.Itoa(port) + "/v1/models",
		Log:         filepath.Join(t.TempDir(), "logs", "model.log"),
		StopGraceMS: 300,
	}
}

func shell(script string) []string {
	return []string{"sh", "-c", script}
}

func testExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	require.NoError(t, err)
	return path
}

func processRunning(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	_, err := server.StartTicks(context.Background(), pid)
	return err == nil
}

func requireGone(t *testing.T, pid int) {
	t.Helper()
	require.Eventually(t, func() bool { return !processRunning(pid) }, 5*time.Second, 20*time.Millisecond, "process %d is still running", pid)
}

func waitForRecord(t *testing.T, store *server.Store, name string) server.Record {
	t.Helper()
	var rec server.Record
	require.Eventually(t, func() bool {
		var err error
		rec, err = store.Read(name)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond, "no record for %s", name)
	return rec
}

func startSleeper(t *testing.T, name string) (*exec.Cmd, server.Record) {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	ticks, err := server.StartTicks(t.Context(), cmd.Process.Pid)
	require.NoError(t, err)
	rec := server.Record{
		Name:       name,
		Kind:       server.KindBrowser,
		PID:        cmd.Process.Pid,
		Port:       9222,
		Command:    []string{"sleep", "60"},
		StartedAt:  time.Now(),
		StartTicks: ticks,
	}
	requireAliveEventually(t, rec)
	return cmd, rec
}

func requireAliveEventually(t *testing.T, rec server.Record) {
	t.Helper()
	store := newStore(t)
	require.Eventually(t, func() bool { return store.Alive(t.Context(), rec) }, 5*time.Second, 5*time.Millisecond, "the helper process never matched its record")
}

func shellProcess(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	})
	return cmd
}

func goroutinesNow() int {
	return runtime.NumGoroutine()
}
