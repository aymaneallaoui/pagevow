//go:build !linux && !windows

package server

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const psTimeout = 2 * time.Second

func inspect(pid int) procInfo {
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-o", "stat=", "-o", "pgid=", "-o", "command=", "-p", strconv.Itoa(pid)) //nolint:gosec // pid is an integer
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return procInfo{}
	}
	line, _, _ := strings.Cut(strings.TrimRight(string(out), "\n"), "\n")
	info, ok := parsePS(line)
	if !ok {
		return procInfo{Exists: strings.TrimSpace(line) != ""}
	}
	return info
}

func groupMembers(pgid int) []groupMember {
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=", "-o", "ppid=", "-o", "pgid=", "-o", "sess=", "-o", "stat=", "-o", "lstart=")
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var members []groupMember
	for _, line := range strings.Split(string(out), "\n") {
		if member, group, ok := parseGroupLine(line); ok && group == pgid {
			members = append(members, member)
		}
	}
	return members
}
