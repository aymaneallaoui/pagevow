package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUseCascadeStoresOpConf(t *testing.T) {
	for arg, want := range map[string]float64{"--op-conf=0": 0, "--op-conf=0.99": 0.99, "--op-conf=1": 1} {
		h := newHarness(t)
		h.mustRun("use", "cascade", "--primary", "http://127.0.0.1:9001", arg)
		assert.InDelta(t, want, h.loadConfig().Backends.Cascade.OpConf, 1e-9, arg)
		assert.Contains(t, h.mustRun("status"), "op_conf")
	}
}

func TestUseCascadeRejectsOutOfRangeOpConf(t *testing.T) {
	for _, value := range []string{"-0.1", "1.5"} {
		h := newHarness(t)
		_, err := h.run("use", "cascade", "--op-conf", value)
		assert.ErrorContains(t, err, "op_conf", value)
	}
}
