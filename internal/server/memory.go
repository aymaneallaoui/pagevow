package server

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
)

const bytesPerMiB = 1 << 20

// unifiedMemoryNames are the sysctl values the unified memory reader asks for, in the order of its output lines.
var unifiedMemoryNames = []string{
	"hw.memsize", "hw.pagesize", "vm.page_free_count", "vm.page_speculative_count", "vm.page_purgeable_count",
	"vm.page_pageable_external_count",
}

// UnifiedMemoryArgs returns the arguments of the one sysctl call that reads the unified memory of a Mac.
func UnifiedMemoryArgs() []string {
	return append([]string{"-n"}, unifiedMemoryNames...)
}

// ParseUnifiedMemory reads the answer of sysctl -n with the arguments of UnifiedMemoryArgs, one value per line.
// Free memory is the free, purgeable and file-backed pages; speculative pages are shown but not added.
func ParseUnifiedMemory(out string) (GPU, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != len(unifiedMemoryNames) {
		return GPU{}, fmt.Errorf("parse unified memory: want %d lines, got %d in %q", len(unifiedMemoryNames), len(lines), out)
	}
	values := make([]uint64, len(lines))
	for i, line := range lines {
		value, err := strconv.ParseUint(strings.TrimSpace(line), 10, 64)
		if err != nil {
			return GPU{}, fmt.Errorf("parse unified memory: %s is %q, not a number", unifiedMemoryNames[i], strings.TrimSpace(line))
		}
		values[i] = value
	}
	total, pageSize := values[0], values[1]
	if total == 0 || pageSize == 0 {
		return GPU{}, errors.New("parse unified memory: hw.memsize and hw.pagesize must not be 0")
	}
	var pages uint64
	// XNU counts a speculative page in vm.page_pageable_external_count too, so adding it would count it twice.
	for _, count := range []uint64{values[2], values[4], values[5]} {
		var carry uint64
		pages, carry = bits.Add64(pages, count, 0)
		if carry != 0 {
			return GPU{}, errors.New("parse unified memory: the page counts overflow")
		}
	}
	high, free := bits.Mul64(pages, pageSize)
	if high != 0 {
		return GPU{}, errors.New("parse unified memory: the free memory overflows")
	}
	free = min(free, total)
	totalMiB, err := toMiB(total)
	if err != nil {
		return GPU{}, err
	}
	freeMiB, err := toMiB(free)
	if err != nil {
		return GPU{}, err
	}
	parts, err := memoryParts(values[2:], pageSize)
	if err != nil {
		return GPU{}, err
	}
	return GPU{TotalMiB: totalMiB, UsedMiB: totalMiB - freeMiB, FreeMiB: freeMiB, Unified: true, Parts: parts}, nil
}

func memoryParts(counts []uint64, pageSize uint64) (MemoryParts, error) {
	var mib [4]int
	for i, count := range counts {
		high, bytes := bits.Mul64(count, pageSize)
		if high != 0 {
			return MemoryParts{}, errors.New("parse unified memory: a page count overflows")
		}
		value, err := toMiB(bytes)
		if err != nil {
			return MemoryParts{}, err
		}
		mib[i] = value
	}
	return MemoryParts{FreeMiB: mib[0], SpeculativeMiB: mib[1], PurgeableMiB: mib[2], FileBackedMiB: mib[3]}, nil
}

func toMiB(bytes uint64) (int, error) {
	mib := bytes / bytesPerMiB
	if mib > math.MaxInt32 {
		return 0, fmt.Errorf("parse unified memory: %d bytes is out of range", bytes)
	}
	return int(mib), nil
}
