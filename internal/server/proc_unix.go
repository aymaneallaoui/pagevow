//go:build !windows

package server

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
)

type groupMember struct {
	PID        int
	PPID       int
	PGID       int
	SID        int
	StartTicks uint64
	Zombie     bool
}

var errUnsafeGroup = errors.New("refusing to signal that process group")

func signalTerm(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

func signalKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }

func safeGroup(pgid int) error {
	if pgid <= 1 || pgid == syscall.Getpgrp() {
		return fmt.Errorf("%w: %d", errUnsafeGroup, pgid)
	}
	return nil
}

func terminateGroup(pgid int) error {
	if err := safeGroup(pgid); err != nil {
		return err
	}
	return syscall.Kill(-pgid, syscall.SIGTERM)
}

func killGroup(pgid int) error {
	if err := safeGroup(pgid); err != nil {
		return err
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}

// killProcessTree kills the process group of pid when pid leads its own group, and pid alone otherwise.
func killProcessTree(pid int) error {
	if inspect(pid).PGID == pid && safeGroup(pid) == nil {
		return syscall.Kill(-pid, syscall.SIGKILL)
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}

func groupExists(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func isGone(err error) bool {
	return errors.Is(err, syscall.ESRCH)
}

// groupTied reports whether the recorded process group still exists and has a member that belongs to the record.
func groupTied(rec Record) bool {
	if safeGroup(rec.ChildPGID) != nil || !groupExists(rec.ChildPGID) {
		return false
	}
	return tiedToRecord(rec, groupMembers(rec.ChildPGID), runtime.GOOS == "darwin")
}

// tiedToRecord decides from a process table whether a live member of the recorded group belongs to the record. On macOS
// a reparented member has no usable session id, so one that started at or after the recorded child counts unless the group
// id was reused.
func tiedToRecord(rec Record, table []groupMember, darwin bool) bool {
	byStart := darwin && rec.ChildStartTicks != 0 && !leaderReused(rec, table)
	for _, member := range table {
		if member.PGID != rec.ChildPGID || member.Zombie {
			continue
		}
		if rec.ChildStartTicks != 0 && member.StartTicks != 0 && member.StartTicks < rec.ChildStartTicks {
			continue
		}
		if member.PPID == rec.PID || (rec.ChildPID > 1 && member.PPID == rec.ChildPID) || (member.SID != 0 && member.SID == rec.PID) {
			return true
		}
		if byStart && member.StartTicks >= rec.ChildStartTicks {
			return true
		}
	}
	return false
}

func leaderReused(rec Record, table []groupMember) bool {
	for _, member := range table {
		if member.PID == rec.ChildPGID && member.PGID == rec.ChildPGID && member.StartTicks != rec.ChildStartTicks {
			return true
		}
	}
	return false
}
