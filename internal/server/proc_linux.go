//go:build linux

package server

import (
	"os"
	"strconv"
	"strings"
)

const (
	statFieldPPID    = 4
	statFieldSession = 6
)

func inspect(pid int) procInfo {
	root := "/proc/" + strconv.Itoa(pid)
	stat, err := os.ReadFile(root + "/stat") //nolint:gosec // pid is an integer
	if err != nil {
		return procInfo{}
	}
	info := procInfo{Exists: true}
	state, pgid, ticks, ok := parseStat(string(stat))
	if !ok {
		return info
	}
	info.Zombie = state == "Z" || state == "X"
	info.PGID = pgid
	info.StartTicks = ticks
	info.TicksKnown = true

	if raw, err := os.ReadFile(root + "/cmdline"); err == nil { //nolint:gosec // pid is an integer
		trimmed := strings.TrimSuffix(string(raw), "\x00")
		info.Args = []string{}
		if trimmed != "" {
			info.Args = strings.Split(trimmed, "\x00")
		}
		info.CmdText = strings.Join(info.Args, " ")
		info.CmdlineKnown = true
	}
	return info
}

func groupMembers(pgid int) []groupMember {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var members []groupMember
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat") //nolint:gosec // the name is an integer
		if err != nil {
			continue
		}
		member, group, ok := parseStatIDs(string(stat))
		if !ok || group != pgid {
			continue
		}
		member.PID = pid
		member.PGID = group
		members = append(members, member)
	}
	return members
}

// parseStatIDs reads state, parent, session and start time from /proc/<pid>/stat.
func parseStatIDs(stat string) (member groupMember, pgid int, ok bool) {
	end := strings.LastIndex(stat, ") ")
	if end < 0 {
		return groupMember{}, 0, false
	}
	fields := strings.Fields(stat[end+2:])
	if len(fields) < statFieldStartTicks-statFirstAfterComm+1 {
		return groupMember{}, 0, false
	}
	ppid, err1 := strconv.Atoi(fields[statFieldPPID-statFirstAfterComm])
	pgid, err2 := strconv.Atoi(fields[statFieldPGRP-statFirstAfterComm])
	sid, err3 := strconv.Atoi(fields[statFieldSession-statFirstAfterComm])
	ticks, err4 := strconv.ParseUint(fields[statFieldStartTicks-statFirstAfterComm], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return groupMember{}, 0, false
	}
	return groupMember{PPID: ppid, SID: sid, StartTicks: ticks, Zombie: fields[0] == "Z" || fields[0] == "X"}, pgid, true
}
