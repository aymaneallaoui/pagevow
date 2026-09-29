//go:build !windows

package server_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func TestParseGroupLine(t *testing.T) {
	started := uint64(time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local).Unix())
	cases := map[string]struct {
		line string
		want server.GroupLine
		ok   bool
	}{
		"single spaces": {"1234 1 1234 1234 Ss Tue Sep 30 10:00:00 2026",
			server.GroupLine{PID: 1234, PPID: 1, PGID: 1234, SID: 1234, StartTicks: started}, true},
		"macos right aligned": {"  1234     1  1234  1234 Ss     Tue Sep 30 10:00:00 2026",
			server.GroupLine{PID: 1234, PPID: 1, PGID: 1234, SID: 1234, StartTicks: started}, true},
		"padded day": {"  77  1234  1234  1234 S+   Wed Oct  7 10:00:00 2026",
			server.GroupLine{PID: 77, PPID: 1234, PGID: 1234, SID: 1234, StartTicks: uint64(time.Date(2026, 10, 7, 10, 0, 0, 0, time.Local).Unix())}, true},
		"zombie": {"99 1234 1234 1234 Z+ Tue Sep 30 10:00:00 2026",
			server.GroupLine{PID: 99, PPID: 1234, PGID: 1234, SID: 1234, StartTicks: started, Zombie: true}, true},
		"different session": {"5 4 100 7 S Tue Sep 30 10:00:00 2026",
			server.GroupLine{PID: 5, PPID: 4, PGID: 100, SID: 7, StartTicks: started}, true},
		"header":          {"  PID  PPID  PGID  SESS STAT STARTED", server.GroupLine{}, false},
		"blank":           {"", server.GroupLine{}, false},
		"missing start":   {"5 4 100 7 S", server.GroupLine{}, false},
		"bad date":        {"5 4 100 7 S Xxx Sep 30 10:00:00 2026", server.GroupLine{}, false},
		"too few numbers": {"5 4 S Tue Sep 30 10:00:00 2026", server.GroupLine{}, false},
	}
	for name, tc := range cases {
		got, ok := server.ParseGroupLine(tc.line)
		require.Equal(t, tc.ok, ok, name)
		if ok {
			assert.Equal(t, tc.want, got, name)
		}
	}
}
