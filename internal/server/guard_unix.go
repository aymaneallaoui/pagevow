//go:build !windows

package server

import "time"

const (
	defaultGuardInterval = time.Second
	unifiedGuardNote     = "guard: free memory guard is off on unified memory until measured"
)

func (g Guard) interval() time.Duration {
	if g.IntervalMS <= 0 {
		return defaultGuardInterval
	}
	return time.Duration(g.IntervalMS) * time.Millisecond
}
