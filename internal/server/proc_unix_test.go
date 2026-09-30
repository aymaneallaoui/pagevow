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
