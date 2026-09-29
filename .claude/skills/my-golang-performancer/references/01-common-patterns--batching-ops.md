Source: https://goperf.dev/01-common-patterns/batching-ops/

# Batching Operations in Go

Batching is one of those techniques that’s easy to overlook but incredibly useful when performance starts to matter. Instead of handling one operation at a time, you group them together—cutting down on the overhead of repeated calls, whether that’s hitting the network, writing to disk, or making a database commit. It’s a practical, low-complexity approach that can reduce latency and stretch your system’s throughput further than you’d expect.

## Why Batching Matters

Most systems don’t struggle because individual operations are too slow—they struggle because they do too many of them. Every call out to a database, API, or filesystem adds some fixed cost: a system call, a network round trip, maybe a lock or a context switch. When those costs add up across high-volume workloads, the impact is hard to ignore. Batching helps by collapsing those calls into fewer, more efficient units of work, which often leads to measurable gains in both performance and resource usage.

Consider a logging service writing to disk:

```
func logLine(line string) {
    f.WriteString(line + "\n")
}
```

When invoked thousands of times per second, the file system is inundated with individual write system calls, significantly degrading performance. A better approach could be aggregates log entries and flushes them in bulk:

```
var batch []string

func logBatch(line string) {
    batch = append(batch, line)
    if len(batch) >= 100 {
        f.WriteString(strings.Join(batch, "\n") + "\n")
        batch = batch[:0]
    }
}
```

With batching, each write operation handles multiple entries simultaneously, reducing syscall overhead and improving disk I/O efficiency.

Warning

While batching offers substantial performance advantages, it also introduces the risk of data loss. If an application crashes before a batch is flushed, the in-memory data can be lost. Systems dealing with critical or transactional data must incorporate safeguards such as periodic flushes, persistent storage buffers, or recovery mechanisms to mitigate this risk.

## How generic Batcher may looks like

We can implement a generic batcher in very straight forward manner:

```
type Batcher[T any] struct {
    mu     sync.Mutex
    buffer []T
    size   int
    flush  func([]T)
}

func NewBatcher[T any](size int, flush func([]T)) *Batcher[T] {
    return &Batcher[T]{
        buffer: make([]T, 0, size),
        size:   size,
        flush:  flush,
    }
}

func (b *Batcher[T]) Add(item T) {
    b.mu.Lock()
    defer b.mu.Unlock()
    b.buffer = append(b.buffer, item)
    if len(b.buffer) >= b.size {
        b.flushNow()
    }
}

func (b *Batcher[T]) flushNow() {
    if len(b.buffer) == 0 {
        return
    }
    b.flush(b.buffer)
    b.buffer = b.buffer[:0]
}
```

Warning

This batcher implementation expects that you will never call `Batcher.Add(...)` from your `flush()` function. We have this limitation because Go mutexes are [**not** recursive](https://stackoverflow.com/questions/14670979/recursive-locking-in-go).

This batcher works with any data type, making it a flexible solution for aggregating logs, metrics, database writes, or other grouped operations. Internally, the buffer acts as a queue that accumulates items until a flush threshold is reached. The use of `sync.Mutex` ensures that `Add()` and `flushNow()` are safe for concurrent access, which is necessary in most real-world systems where multiple goroutines may write to the batcher.

From a performance standpoint, it's true that a lock-free implementation—using atomic operations or concurrent ring buffers—could reduce contention and improve throughput under heavy load. However, such designs are more complex, harder to maintain, and generally not justified unless you're pushing extremely high concurrency or low-latency boundaries. For most practical workloads, the simplicity and safety of a `sync.Mutex`\-based design offers a great balance between performance and maintainability.

## Benchmarking Impact

To validate batching performance, we tested six scenarios across three categories: in-memory processing, file I/O, and CPU-intensive hashing. Each category included both unbatched and batched variants, with all benchmarks running over 10,000 items per operation.

Show the benchmark file

```
package perf

import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "os"
    "strings"
    "testing"
)

var lines = make([]string, 10000)

func init() {
    for i := range lines {
        lines[i] = fmt.Sprintf("log entry %d %s", i, strings.Repeat("x", 100))
    }
}

// --- 1. No I/O ---

func BenchmarkUnbatchedProcessing(b *testing.B) {
    for b.Loop() {
        for _, line := range lines {
            strings.ToUpper(line)
        }
    }
}

func BenchmarkBatchedProcessing(b *testing.B) {
    batchSize := 100
    for b.Loop() {
        for i := 0; i < len(lines); i += batchSize {
            end := i + batchSize
            if end > len(lines) {
                end = len(lines)
            }
            batch := strings.Join(lines[i:end], "|")
            strings.ToUpper(batch)
        }
    }
}

// --- 2. With I/O ---

func BenchmarkUnbatchedIO(b *testing.B) {
    for b.Loop() {
        f, err := os.CreateTemp("", "unbatched")
        if err != nil {
            b.Fatal(err)
        }
        for _, line := range lines {
            _, _ = f.WriteString(line + "\n")
        }
        f.Close()
        os.Remove(f.Name())
    }
}

func BenchmarkBatchedIO(b *testing.B) {
    batchSize := 100
    for b.Loop() {
        f, err := os.CreateTemp("", "batched")
        if err != nil {
            b.Fatal(err)
        }
        for i := 0; i < len(lines); i += batchSize {
            end := i + batchSize
            if end > len(lines) {
                end = len(lines)
            }
            batch := strings.Join(lines[i:end], "\n") + "\n"
            _, _ = f.WriteString(batch)
        }
        f.Close()
        os.Remove(f.Name())
    }
}

// --- 3. With Crypto ---

func hash(s string) string {
    h := sha256.Sum256([]byte(s))
    return hex.EncodeToString(h[:])
}

func BenchmarkUnbatchedCrypto(b *testing.B) {
    for b.Loop() {
        for _, line := range lines {
            hash(line)
        }
    }
}

func BenchmarkBatchedCrypto(b *testing.B) {
    batchSize := 100
    for b.Loop() {
        for i := 0; i < len(lines); i += batchSize {
            end := i + batchSize
            if end > len(lines) {
                end = len(lines)
            }
            joined := strings.Join(lines[i:end], "")
            hash(joined)
        }
    }
}
```

| Benchmark | Iterations | Time per op (ns) | Bytes per op | Allocs per op |
| --- | --- | --- | --- | --- |
| BenchmarkUnbatchedProcessing-14 | 530 | 2,028,492 | 1,279,850 | 10,000 |
| BenchmarkBatchedProcessing-14 | 573 | 2,094,168 | 2,457,603 | 200 |

In-memory string manipulation showed a modest performance delta. While the batched variant reduced memory allocations by 50x, the execution time was only marginally slower due to the cost of joining large strings. This highlights that batching isn’t always faster in raw throughput, but it consistently reduces pressure on the garbage collector.

| Benchmark | Iterations | Time per op (ns) | Bytes per op | Allocs per op |
| --- | --- | --- | --- | --- |
| BenchmarkUnbatchedIO-14 | 87 | 12,766,433 | 1,280,424 | 10,007 |
| BenchmarkBatchedIO-14 | 1324 | 993,912 | 2,458,026 | 207 |

File I/O benchmarks showed the most dramatic gains. The batched version was over 12 times faster than the unbatched one, with far fewer syscalls and significantly lower execution time. Grouping disk writes amortized the I/O cost, leading to a huge efficiency boost despite temporarily using more memory.

| Benchmark | Iterations | Time per op (ns) | Bytes per op | Allocs per op |
| --- | --- | --- | --- | --- |
| BenchmarkUnbatchedCrypto-14 | 978 | 1,232,242 | 2,559,840 | 30,000 |
| BenchmarkBatchedCrypto-14 | 1760 | 675,303 | 2,470,406 | 400 |

The cryptographic benchmarks demonstrated batching’s value in CPU-bound scenarios. Batched hashing nearly halved the total processing time while reducing allocation count by more than 70x. This reinforces batching as an effective strategy even in CPU-intensive workloads where fewer operations yield better locality and cache behavior.

## When To Use Batching

Use batching when:

- Individual operations are expensive (e.g., I/O, RPC, DB writes). Grouping multiple operations into a single batch reduces the overhead of repeated calls and improves efficiency.
- The system benefits from reducing the frequency of external interactions. Fewer external calls can ease load on downstream systems and reduce contention or rate-limiting issues.
- You have some tolerance for per-item latency in favor of higher throughput. Batching introduces slight delays but can significantly increase overall system throughput.

Avoid batching when:

- Immediate action is required for each individual input. Delaying processing to build a batch may violate time-sensitive requirements.
- Holding data introduces risk (e.g., crash before flush). If data must be processed or persisted immediately to avoid loss, batching can be unsafe.
- Predictable latency is more important than throughput. Batching adds variability in timing, which may not be acceptable in systems with strict latency expectations.
