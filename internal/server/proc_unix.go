//go:build !windows

package server

import (
	"context"
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

func signalTerm(_ context.Context, pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

func signalKill(_ context.Context, pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }

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
func killProcessTree(ctx context.Context, pid int) error {
	if inspect(context.WithoutCancel(ctx), pid).PGID == pid && safeGroup(pid) == nil {
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

// orphanLives reports whether the program of a supervised record still runs and can be tied to the record although
// the supervisor fails the alive check.
func (s *Store) orphanLives(ctx context.Context, rec Record) bool {
	if otherBoot(rec) {
		return false
	}
	return orphanTied(rec, s.inspectPID(ctx, rec.ChildPID), func(rec Record) bool { return groupTied(ctx, rec) })
}

// orphanTied decides from the child's process entry and the group tie whether a supervised record has a live orphan;
// without a child start time and a usable group nothing can be proven, so nothing counts.
func orphanTied(rec Record, child procInfo, tied func(Record) bool) bool {
	if !rec.Kind.supervised() || rec.ChildStartTicks == 0 || safeGroup(rec.ChildPGID) != nil {
		return false
	}
	return childMatches(rec, child) || tied(rec)
}

// groupTied reports whether the recorded process group still exists and has a member that belongs to the record.
func groupTied(ctx context.Context, rec Record) bool {
	if safeGroup(rec.ChildPGID) != nil || !groupExists(rec.ChildPGID) {
		return false
	}
	return tiedToRecord(rec, groupMembers(ctx, rec.ChildPGID), runtime.GOOS == "darwin")
}

// tiedToRecord decides from a process table whether a live member of the recorded group belongs to the record. A group
// whose leader started at another time than the recorded child is a reused group id and ties to nothing. On macOS a
// reparented member has no usable session id, so one that started at or after the recorded child counts.
func tiedToRecord(rec Record, table []groupMember, darwin bool) bool {
	if leaderReused(rec, table) {
		return false
	}
	byStart := darwin && rec.ChildStartTicks != 0
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
	if rec.ChildStartTicks == 0 {
		return false
	}
	for _, member := range table {
		if member.PID == rec.ChildPGID && member.PGID == rec.ChildPGID && member.StartTicks != 0 && member.StartTicks != rec.ChildStartTicks {
			return true
		}
	}
	return false
}
