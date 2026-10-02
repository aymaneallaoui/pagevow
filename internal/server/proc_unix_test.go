//go:build !windows

package server_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func TestTiedToRecord(t *testing.T) {
	const (
		supervisor = 500
		leader     = 900
		started    = 1000
	)
	rec := server.Record{PID: supervisor, ChildPID: leader, ChildPGID: leader, ChildStartTicks: started}
	orphan := server.GroupLine{PID: 901, PPID: 1, PGID: leader, StartTicks: started}

	cases := []struct {
		name   string
		rec    server.Record
		table  []server.GroupLine
		darwin bool
		want   bool
	}{
		{"darwin orphan started with the child", rec, []server.GroupLine{orphan}, true, true},
		{"darwin orphan started after the child", rec, []server.GroupLine{{PID: 901, PPID: 1, PGID: leader, StartTicks: started + 30}}, true, true},
		{"darwin member older than the child", rec, []server.GroupLine{{PID: 901, PPID: 1, PGID: leader, StartTicks: started - 1}}, true, false},
		{"darwin member of another group", rec, []server.GroupLine{{PID: 901, PPID: 1, PGID: 777, StartTicks: started + 5}}, true, false},
		{"darwin zombie member", rec, []server.GroupLine{{PID: 901, PPID: 1, PGID: leader, StartTicks: started, Zombie: true}}, true, false},
		{"darwin record without a child start time", server.Record{PID: supervisor, ChildPID: leader, ChildPGID: leader}, []server.GroupLine{orphan}, true, false},
		{"darwin member without a start time", rec, []server.GroupLine{{PID: 901, PPID: 1, PGID: leader}}, true, false},
		{"darwin reused group id with a newer leader", rec, []server.GroupLine{
			{PID: leader, PPID: 1, PGID: leader, StartTicks: started + 60},
			{PID: 902, PPID: 1, PGID: leader, StartTicks: started + 61},
		}, true, false},
		{"darwin leader that matches the child", rec, []server.GroupLine{{PID: leader, PPID: 1, PGID: leader, StartTicks: started}}, true, true},
		{"other systems do not tie by start time", rec, []server.GroupLine{orphan}, false, false},
		{"other systems still tie by parent", rec, []server.GroupLine{{PID: 901, PPID: supervisor, PGID: leader, StartTicks: started}}, false, true},
		{"other systems still tie by session", rec, []server.GroupLine{{PID: 901, PPID: 1, PGID: leader, SID: supervisor, StartTicks: started}}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, server.TiedToRecord(tc.rec, tc.table, tc.darwin))
		})
	}
}

func TestOrphanTied(t *testing.T) {
	const (
		supervisor = 2147480000
		child      = 2147480100
		started    = 1000
		command    = "uv run --extra serve python -m kev.serve --port 8009"
	)
	rec := server.Record{
		Kind: server.KindModel, PID: supervisor, ChildPID: child, ChildPGID: child, ChildStartTicks: started,
		Command: []string{"uv", "run", "--extra", "serve", "python", "-m", "kev.serve", "--port", "8009"},
	}
	running := server.ChildEntry{Running: true, StartTicks: started, PGID: child, Command: command}
	reused := server.ChildEntry{Running: true, StartTicks: started + 40, PGID: child, Command: "sleep 60"}
	gone := server.ChildEntry{}
	member := server.GroupLine{PID: child + 5, PPID: 1, PGID: child, SID: supervisor, StartTicks: started + 1}
	darwinMember := server.GroupLine{PID: child + 5, PPID: 1, PGID: child, StartTicks: started + 1}
	withoutTicks := rec
	withoutTicks.ChildStartTicks = 0
	browser := rec
	browser.Kind = server.KindBrowser
	unsafeGroup := rec
	unsafeGroup.ChildPGID = 1

	cases := []struct {
		name   string
		rec    server.Record
		child  server.ChildEntry
		table  []server.GroupLine
		darwin bool
		want   bool
	}{
		{"linux child with the recorded start time, group and command", rec, running, nil, false, true},
		{"darwin child with the recorded start time, group and command", rec, running, nil, true, true},
		{"linux child pid reused by another process", rec, reused, []server.GroupLine{{PID: child, PPID: 1, PGID: child, SID: child, StartTicks: started + 40}}, false, false},
		{"darwin child pid reused by another process", rec, reused, []server.GroupLine{{PID: child, PPID: 1, PGID: child, StartTicks: started + 40}}, true, false},
		{"linux child with the start time but another command and session", rec, server.ChildEntry{Running: true, StartTicks: started, PGID: child, Command: "sleep 60"}, nil, false, false},
		{"linux child in another group", rec, server.ChildEntry{Running: true, StartTicks: started, PGID: child + 1, Command: command}, nil, false, false},
		{"linux child gone while a member of its group runs in the supervisor session", rec, gone, []server.GroupLine{member}, false, true},
		{"darwin child gone while a member started after it runs", rec, gone, []server.GroupLine{darwinMember}, true, true},
		{"linux child gone and the group empty", rec, gone, nil, false, false},
		{"darwin child gone and the group empty", rec, gone, nil, true, false},
		{"record without a child start time", withoutTicks, running, []server.GroupLine{member}, false, false},
		{"browser record", browser, running, nil, false, false},
		{"record whose group is not one pagevow may signal", unsafeGroup, running, nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, server.OrphanTied(tc.rec, tc.child, tc.table, tc.darwin))
		})
	}
}
