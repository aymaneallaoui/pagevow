package server_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

const sixteenGiBMac = "17179869184\n16384\n64000\n12800\n6400\n"

func TestUnifiedMemoryArgsAskForTheFiveValuesInOneCall(t *testing.T) {
	assert.Equal(t, []string{
		"-n", "hw.memsize", "hw.pagesize", "vm.page_free_count", "vm.page_speculative_count", "vm.page_purgeable_count",
	}, server.UnifiedMemoryArgs())
}

func TestParseUnifiedMemoryCountsFreeSpeculativeAndPurgeablePages(t *testing.T) {
	got, err := server.ParseUnifiedMemory(sixteenGiBMac)
	require.NoError(t, err)
	assert.Equal(t, server.GPU{TotalMiB: 16384, UsedMiB: 15084, FreeMiB: 1300, TempC: 0, Unified: true}, got)
}

func TestParseUnifiedMemoryAcceptsSpacesAndCarriageReturns(t *testing.T) {
	got, err := server.ParseUnifiedMemory(" 17179869184\r\n16384\r\n 64000\r\n12800\r\n6400\r\n")
	require.NoError(t, err)
	assert.Equal(t, 1300, got.FreeMiB)
}

func TestParseUnifiedMemoryNeverReportsMoreFreeThanTotal(t *testing.T) {
	got, err := server.ParseUnifiedMemory("1073741824\n16384\n100000\n0\n0\n")
	require.NoError(t, err)
	assert.Equal(t, server.GPU{TotalMiB: 1024, UsedMiB: 0, FreeMiB: 1024, Unified: true}, got)
}

func TestParseUnifiedMemoryRejectsMalformedOutput(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{"a word", "17179869184\n16384\nlots\n12800\n6400\n", `vm.page_free_count is "lots"`},
		{"a negative count", "17179869184\n16384\n64000\n-1\n6400\n", `vm.page_speculative_count is "-1"`},
		{"a sysctl error line", "17179869184\n16384\n64000\n12800\nsysctl: unknown oid 'vm.page_purgeable_count'\n", "vm.page_purgeable_count"},
		{"no memory size", "0\n16384\n64000\n12800\n6400\n", "must not be 0"},
		{"no page size", "17179869184\n0\n64000\n12800\n6400\n", "must not be 0"},
		{"an overflow", "17179869184\n16384\n18446744073709551615\n1\n0\n", "overflow"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := server.ParseUnifiedMemory(tt.out)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestParseUnifiedMemoryRejectsMissingOrExtraLines(t *testing.T) {
	for name, out := range map[string]string{
		"empty":      "",
		"one line":   "17179869184\n",
		"four lines": "17179869184\n16384\n64000\n12800\n",
		"blank line": "17179869184\n16384\n\n12800\n6400\n",
		"six lines":  sixteenGiBMac + "7\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := server.ParseUnifiedMemory(out)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "parse unified memory")
		})
	}
}
