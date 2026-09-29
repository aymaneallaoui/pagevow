//go:build !windows

package server

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
