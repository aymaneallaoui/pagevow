//go:build !windows

package server_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

// TestMain lets the test binary act as the `supervise` command so Spawn can start it as a real detached supervisor.
func TestMain(m *testing.M) {
	switch os.Getenv("SERVER_TEST_MODE") {
	case "fake-supervisor":
		os.Exit(runFakeSupervisor(os.Getenv("SERVER_TEST_DIR")))
	case "leader":
		os.Exit(runLeader(os.Getenv("SERVER_TEST_DIR")))
	case "sleeper":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if len(os.Args) > 3 && os.Args[1] == "supervise" && os.Args[2] == "--spec" {
		os.Exit(runSupervisor(os.Args[3]))
	}
	os.Exit(m.Run())
}

func runSupervisor(specPath string) int {
	spec, err := server.ReadSpec(specPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var gpu server.GPUSource
	if reading := os.Getenv("SERVER_TEST_GPU"); reading != "" {
		gpu = server.NvidiaSMI{Run: func(context.Context, string, ...string) ([]byte, error) {
			return []byte(reading), nil
		}}
	}
	code, err := server.Supervise(context.Background(), spec, server.NewStore(filepath.Dir(specPath)), gpu)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return code
}

// runFakeSupervisor ignores SIGTERM and, when dir is set, starts a group leader that leaves a member behind.
func runFakeSupervisor(dir string) int {
	signal.Ignore(syscall.SIGTERM)
	if dir != "" {
		leader := exec.Command(os.Args[0])
		leader.Env = append(os.Environ(), "SERVER_TEST_MODE=leader")
		leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := leader.Start(); err != nil {
			return 1
		}
		writePID(filepath.Join(dir, "leader"), leader.Process.Pid)
		go func() { _ = leader.Wait() }()
	}
	time.Sleep(time.Hour)
	return 0
}

func runLeader(dir string) int {
	member := exec.Command("sleep", "60")
	if err := member.Start(); err != nil {
		return 1
	}
	writePID(filepath.Join(dir, "member"), member.Process.Pid)
	return 0
}

func writePID(path string, pid int) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)), 0o600); err == nil {
		_ = os.Rename(tmp, path)
	}
}
