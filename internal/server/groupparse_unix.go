//go:build !windows

package server

import (
	"strconv"
	"strings"
	"time"
)

// parseGroupLine reads one line of `ps -A -o pid= -o ppid= -o pgid= -o sess= -o stat= -o lstart=`; lstart comes last because it holds spaces.
func parseGroupLine(line string) (member groupMember, pgid int, ok bool) {
	var numbers [4]int
	rest := line
	for i := range numbers {
		var field string
		field, rest = cutField(rest)
		value, err := strconv.Atoi(field)
		if err != nil {
			return groupMember{}, 0, false
		}
		numbers[i] = value
	}
	state, rest := cutField(rest)
	rest = strings.TrimLeft(rest, " \t")
	if state == "" || len(rest) < len(psStartLayout) {
		return groupMember{}, 0, false
	}
	started, err := time.ParseInLocation(psStartLayout, rest[:len(psStartLayout)], time.Local)
	if err != nil {
		return groupMember{}, 0, false
	}
	return groupMember{
		PID:        numbers[0],
		PPID:       numbers[1],
		SID:        numbers[3],
		StartTicks: uint64(started.Unix()), //nolint:gosec // dates after 1970 are positive
		Zombie:     strings.HasPrefix(state, "Z"),
	}, numbers[2], true
}
