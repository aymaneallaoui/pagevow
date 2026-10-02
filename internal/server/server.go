// Package server manages the local processes pagevow starts: model servers, the text helper and the browser.
package server

import (
	"errors"
	"fmt"
)

// Kind tells which program a record belongs to.
type Kind string

// The kinds of process pagevow manages.
const (
	KindModel      Kind = "model"
	KindTextHelper Kind = "text-helper"
	KindBrowser    Kind = "browser"
)

// GuardExitCode is the exit status of a supervisor that stopped its child because of the GPU guard.
const GuardExitCode = 99

// Errors returned by this package.
var (
	ErrUnsupported    = errors.ErrUnsupported
	ErrNotFound       = errors.New("record not found")
	ErrProcessEnded   = errors.New("process ended")
	ErrAlreadyRunning = errors.New("a record for that name still runs")
)

func (k Kind) valid() bool {
	return k == KindModel || k == KindTextHelper || k == KindBrowser
}

func (k Kind) supervised() bool {
	return k == KindModel || k == KindTextHelper
}

// RecordName returns the record name for a process of the given kind that listens on port.
func RecordName(kind Kind, port int) string {
	return fmt.Sprintf("%s-%d", kind, port)
}

func validName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}
