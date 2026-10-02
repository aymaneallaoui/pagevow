//go:build !windows

package server

import "strings"

// GroupLine is one process line of the group listing, as the tests see it.
type GroupLine struct {
	PID, PPID, PGID, SID int
	StartTicks           uint64
	Zombie               bool
}

// ParseGroupLine exposes the ps group line parser to the tests.
var ParseGroupLine = func(line string) (GroupLine, bool) {
	member, pgid, ok := parseGroupLine(line)
	return GroupLine{member.PID, member.PPID, pgid, member.SID, member.StartTicks, member.Zombie}, ok
}

// TiedToRecord exposes the tie decision of a process table to the tests.
var TiedToRecord = func(rec Record, table []GroupLine, darwin bool) bool {
	members := make([]groupMember, len(table))
	for i, line := range table {
		members[i] = groupMember(line)
	}
	return tiedToRecord(rec, members, darwin)
}

// ChildEntry is the process table entry of a recorded child, as the tests see it.
type ChildEntry struct {
	Running    bool
	StartTicks uint64
	PGID       int
	Command    string
}

// OrphanTied exposes the orphan decision of a process table to the tests; on darwin the command is one text, as ps prints it.
var OrphanTied = func(rec Record, child ChildEntry, table []GroupLine, darwin bool) bool {
	info := procInfo{Exists: child.Running, StartTicks: child.StartTicks, TicksKnown: child.Running, PGID: child.PGID, CmdlineKnown: child.Running}
	if darwin {
		info.CmdText = child.Command
	} else {
		info.Args = strings.Fields(child.Command)
	}
	members := make([]groupMember, len(table))
	for i, line := range table {
		members[i] = groupMember(line)
	}
	return orphanTied(rec, info, func(r Record) bool { return tiedToRecord(r, members, darwin) })
}
