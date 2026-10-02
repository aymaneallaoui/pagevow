package server_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func TestBootChanged(t *testing.T) {
	cases := []struct {
		name              string
		recorded, current string
		want              bool
	}{
		{"same boot", "abc", "abc", false},
		{"another boot", "abc", "def", true},
		{"record without a boot id", "", "def", false},
		{"system without a boot id", "abc", "", false},
		{"neither has one", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, server.BootChanged(tc.recorded, tc.current))
		})
	}
}

func TestSignalFailedIgnoresAProcessThatIsGone(t *testing.T) {
	taskkillFailure := errors.New("taskkill: exit status 128")
	cases := []struct {
		name  string
		err   error
		alive bool
		want  bool
	}{
		{"signal sent", nil, true, false},
		{"signal failed and the process still runs", taskkillFailure, true, true},
		{"signal failed and the process is gone, as on Windows when the browser exits first", taskkillFailure, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, server.SignalFailed(tc.err, func() bool { return tc.alive }))
		})
	}
}
