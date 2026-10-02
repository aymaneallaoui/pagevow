package server_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

const sixteenGiBMac = "17179869184\n16384\n64000\n12800\n6400\n25600\n"

func TestUnifiedMemoryArgsAskForTheSixValuesInOneCall(t *testing.T) {
	assert.Equal(t, []string{
		"-n", "hw.memsize", "hw.pagesize", "vm.page_free_count", "vm.page_speculative_count", "vm.page_purgeable_count",
		"vm.page_pageable_external_count",
	}, server.UnifiedMemoryArgs())
}

func TestParseUnifiedMemoryCountsWhatMacOSCanReclaim(t *testing.T) {
	got, err := server.ParseUnifiedMemory(sixteenGiBMac)
	require.NoError(t, err)
	want := server.GPU{
		TotalMiB: 16384, UsedMiB: 14884, FreeMiB: 1500, TempC: 0, Unified: true,
		Parts: server.MemoryParts{FreeMiB: 1000, SpeculativeMiB: 200, PurgeableMiB: 100, FileBackedMiB: 400},
	}
	assert.Equal(t, want, got)
}

func TestParseUnifiedMemoryAcceptsSpacesAndCarriageReturns(t *testing.T) {
	got, err := server.ParseUnifiedMemory(" 17179869184\r\n16384\r\n 64000\r\n12800\r\n6400\r\n 25600\r\n")
	require.NoError(t, err)
	assert.Equal(t, 1500, got.FreeMiB)
}

func TestParseUnifiedMemoryDoesNotCountSpeculativePagesTwice(t *testing.T) {
	got, err := server.ParseUnifiedMemory("17179869184\n16384\n64000\n12800\n0\n12800\n")
	require.NoError(t, err)
	assert.Equal(t, 1200, got.FreeMiB, "speculative pages are already in the file-backed count")
	assert.Equal(t, 200, got.Parts.SpeculativeMiB)
	assert.Equal(t, 200, got.Parts.FileBackedMiB)
}

func TestParseUnifiedMemoryNeverReportsMoreFreeThanTotal(t *testing.T) {
	got, err := server.ParseUnifiedMemory("1073741824\n16384\n100000\n0\n0\n0\n")
	require.NoError(t, err)
	assert.Equal(t, server.GPU{TotalMiB: 1024, UsedMiB: 0, FreeMiB: 1024, Unified: true, Parts: server.MemoryParts{FreeMiB: 1562}}, got)
}

func TestParseUnifiedMemoryRejectsMalformedOutput(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{"a word", "17179869184\n16384\nlots\n12800\n6400\n25600\n", `vm.page_free_count is "lots"`},
		{"a negative count", "17179869184\n16384\n64000\n-1\n6400\n25600\n", `vm.page_speculative_count is "-1"`},
		{"a sysctl error line", "17179869184\n16384\n64000\n12800\n6400\nsysctl: unknown oid 'vm.page_pageable_external_count'\n", "vm.page_pageable_external_count"},
		{"no memory size", "0\n16384\n64000\n12800\n6400\n25600\n", "must not be 0"},
		{"no page size", "17179869184\n0\n64000\n12800\n6400\n25600\n", "must not be 0"},
		{"an overflow", "17179869184\n16384\n18446744073709551615\n0\n1\n0\n", "overflow"},
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
		"empty":       "",
		"one line":    "17179869184\n",
		"five lines":  "17179869184\n16384\n64000\n12800\n6400\n",
		"blank line":  "17179869184\n16384\n\n12800\n6400\n25600\n",
		"seven lines": sixteenGiBMac + "7\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := server.ParseUnifiedMemory(out)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "parse unified memory")
		})
	}
}
