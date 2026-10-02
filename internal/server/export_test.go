package server

// ParsePS exposes the ps line parser to the tests.
var ParsePS = func(line string) (started uint64, pgid int, zombie bool, text string, ok bool) {
	info, ok := parsePS(line)
	return info.StartTicks, info.PGID, info.Zombie, info.CmdText, ok
}

// ParseStat exposes the /proc stat parser to the tests.
var ParseStat = func(stat string) (state string, pgid int, ticks uint64, ok bool) {
	return parseStat(stat)
}

// MatchArgs exposes the command line matching of the alive check to the tests.
var MatchArgs = func(args []string, first, contains string) (firstIs, has bool) {
	info := procInfo{Args: args}
	return info.firstArgIs(first), info.hasArg(contains)
}

// GuardMessage exposes the guard message of one sample to the tests.
var GuardMessage = func(g Guard, name string, sample GPU) string {
	return g.breachMessage(name, sample)
}

// NoReaderMessage exposes the guard line for a missing memory reader to the tests.
var NoReaderMessage = noReaderMessage

// BootChanged exposes the boot comparison of the alive check to the tests.
var BootChanged = bootChanged

// SignalFailed exposes the decision whether a failed signal is an error to the tests.
var SignalFailed = signalFailed
