package server

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// procInfo is what the operating system tells about one process; the *Known fields say which parts this system can provide.
type procInfo struct {
	Exists       bool
	Zombie       bool
	StartTicks   uint64
	TicksKnown   bool
	PGID         int
	Args         []string
	CmdText      string
	CmdlineKnown bool
}

func (p procInfo) running() bool {
	return p.Exists && !p.Zombie
}

// A process that rewrites its title, as Chromium does, shows its whole command line as one element separated by spaces.
func (p procInfo) firstArgIs(arg string) bool {
	if p.Args != nil {
		return len(p.Args) > 0 && (p.Args[0] == arg || (len(p.Args) == 1 && strings.HasPrefix(p.Args[0], arg+" ")))
	}
	return p.CmdText == arg || strings.HasPrefix(p.CmdText, arg+" ")
}

func (p procInfo) hasArg(arg string) bool {
	if p.Args != nil {
		return slices.Contains(p.Args, arg) || (len(p.Args) == 1 && strings.Contains(" "+p.Args[0]+" ", " "+arg+" "))
	}
	return strings.Contains(p.CmdText, arg)
}

// Alive reports whether rec still names the process pagevow started: the pid exists, its start time is the recorded one and its command line fits the kind.
func (s *Store) Alive(rec Record) bool {
	if !rec.Kind.valid() || !usablePID(rec.PID) {
		return false
	}
	info := inspect(rec.PID)
	if !info.running() {
		return false
	}
	if rec.StartTicks != 0 && info.TicksKnown && info.StartTicks != rec.StartTicks {
		return false
	}
	if !info.CmdlineKnown {
		return true
	}
	if rec.Kind.supervised() {
		return info.hasArg("supervise") && info.hasArg(s.SpecPath(rec.Name))
	}
	if len(rec.Command) == 0 {
		return false
	}
	if info.firstArgIs(rec.Command[0]) {
		return true
	}
	return rec.ProfileDir != "" && info.hasArg("--user-data-dir="+rec.ProfileDir)
}

// ChildAlive reports whether the managed program of a supervised record is still the process that was started.
func ChildAlive(rec Record) bool {
	if !rec.Kind.supervised() || !usablePID(rec.ChildPID) {
		return false
	}
	info := inspect(rec.ChildPID)
	if !info.running() {
		return false
	}
	if rec.ChildStartTicks != 0 && info.TicksKnown && info.StartTicks != rec.ChildStartTicks {
		return false
	}
	return info.PGID == 0 || rec.ChildPGID == 0 || info.PGID == rec.ChildPGID
}

// StartTicks returns the start time of pid in the unit the record stores; it is 0 where the system has none.
func StartTicks(pid int) (uint64, error) {
	info := inspect(pid)
	if !info.running() {
		return 0, fmt.Errorf("process %d is not running", pid)
	}
	return info.StartTicks, nil
}

func usablePID(pid int) bool {
	return pid > 1 && pid != os.Getpid()
}
