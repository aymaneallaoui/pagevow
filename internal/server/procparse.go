package server

import (
	"strconv"
	"strings"
	"time"
)

const (
	statFieldPGRP       = 5
	statFieldStartTicks = 22
	statFirstAfterComm  = 3
	psStartLayout       = "Mon Jan _2 15:04:05 2006"
)

// parsePS reads one line of `ps -o lstart= -o stat= -o pgid= -o command=`, where lstart is always 24 characters wide.
func parsePS(line string) (procInfo, bool) {
	line = strings.TrimLeft(line, " ")
	if len(line) < len(psStartLayout) {
		return procInfo{}, false
	}
	started, err := time.ParseInLocation(psStartLayout, line[:len(psStartLayout)], time.Local)
	if err != nil {
		return procInfo{}, false
	}
	state, rest := cutField(line[len(psStartLayout):])
	pgidText, command := cutField(rest)
	pgid, err := strconv.Atoi(pgidText)
	if state == "" || err != nil {
		return procInfo{}, false
	}
	return procInfo{
		Exists:       true,
		Zombie:       strings.HasPrefix(state, "Z"),
		StartTicks:   uint64(started.Unix()), //nolint:gosec // dates after 1970 are positive
		TicksKnown:   true,
		PGID:         pgid,
		CmdText:      strings.TrimSpace(command),
		CmdlineKnown: true,
	}, true
}

func cutField(text string) (field, rest string) {
	text = strings.TrimLeft(text, " \t")
	end := strings.IndexAny(text, " \t")
	if end < 0 {
		return text, ""
	}
	return text[:end], text[end:]
}

// parseStat reads state, process group and start time from /proc/<pid>/stat, whose second field may itself contain spaces and parentheses.
func parseStat(stat string) (state string, pgid int, ticks uint64, ok bool) {
	end := strings.LastIndex(stat, ") ")
	if end < 0 {
		return "", 0, 0, false
	}
	fields := strings.Fields(stat[end+2:])
	if len(fields) < statFieldStartTicks-statFirstAfterComm+1 {
		return "", 0, 0, false
	}
	pgid, err := strconv.Atoi(fields[statFieldPGRP-statFirstAfterComm])
	if err != nil {
		return "", 0, 0, false
	}
	ticks, err = strconv.ParseUint(fields[statFieldStartTicks-statFirstAfterComm], 10, 64)
	if err != nil {
		return "", 0, 0, false
	}
	return fields[0], pgid, ticks, true
}
