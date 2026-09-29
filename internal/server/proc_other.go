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
	cmd := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=", "-o", "ppid=", "-o", "pgid=", "-o", "stat=")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var members []groupMember
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		group, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil || group != pgid {
			continue
		}
		members = append(members, groupMember{PID: pid, PPID: ppid, Zombie: strings.HasPrefix(fields[3], "Z")})
	}
	return members
}
