Source: https://goperf.dev/01-common-patterns/buffered-io/

# Efficient Buffering in Go

Buffering is a core performance technique in systems programming. In Go, it's especially relevant when working with I/O—file access, network communication, and stream processing. Without buffering, many operations incur excessive system calls or synchronization overhead. Proper buffering reduces the frequency of such interactions, improves throughput, and smooths latency spikes.

## Why Buffering Matters

Every time you read from or write to a file or socket, there’s a good chance you’re triggering a system call—and that’s not cheap. System calls move control from user space into kernel space, which means crossing a boundary that comes with overhead: entering kernel mode, possible context switches, interacting with I/O buffers, and sometimes queuing operations behind the scenes. Doing that once in a while is fine. Doing it thousands of times per second? That’s a problem. Buffering helps by batching small reads or writes into larger chunks, reducing how often you cross that boundary and making far better use of each syscall.

For example, writing to a file in a loop without buffering, like this:

```
f, _ := os.Create("output.txt")
for i := 0; i < 10000; i++ {
    f.Write([]byte("line\n"))
}
```

This can easily result in **10,000 separate system calls**, each carrying its own overhead and dragging down performance. On top of that, a flood of small writes tends to fragment disk operations, which puts extra pressure on I/O subsystems and wastes CPU cycles handling what could have been a single, efficient batch.

### With Buffering

```
f, _ := os.Create("output.txt")
buf := bufio.NewWriter(f)
for i := 0; i < 10000; i++ {
    buf.WriteString("line\n")
}
buf.Flush() // ensure all buffered data is written
```

This version significantly reduces the number of system calls. The `bufio.Writer` accumulates writes in an internal memory buffer (typically 4KB or more). It only triggers a syscall when the buffer is full or explicitly flushed. As a result, you achieve faster I/O, reduced CPU usage, and improved performance.

Note

`bufio.Writer` does not automatically flush when closed. If you forget to call `Flush()`, any unwritten data remaining in the buffer will be lost. Always call `Flush()` before closing or returning from a function, especially if the total written size is smaller than the buffer capacity.

### Controlling Buffer Capacity

By default, `bufio.NewWriter()` allocates a 4096-byte (4 KB) buffer. This size aligns with the common block size of file systems and the standard memory page size on most operating systems (such as Linux, BSD, and macOS). Reading or writing in 4 KB increments minimizes page faults, aligns with kernel read-ahead strategies, and maps efficiently onto underlying disk I/O operations.

While 4 KB is a practical general-purpose default, it might not be optimal for all workloads. For high-throughput scenarios—such as streaming large files or generating extensive logs—a larger buffer can help reduce syscall frequency further:

```
f, _ := os.Create("output.txt")
buf := bufio.NewWriterSize(f, 16*1024) // 16 KB buffer
```

Conversely, if latency is more critical than throughput (e.g., interactive systems or command-line utilities), a smaller buffer may be more appropriate, as it flushes data more frequently.

Similar logic applies when reading data:

```
reader := bufio.NewReaderSize(f, 32*1024) // 32 KB buffer for input
```

Buffer size isn’t something to guess at—it’s something to measure. The ideal size depends on too many variables to hard-code: whether you’re writing to SSDs or spinning disks, how your filesystem buffers writes, how much CPU cache is available, and what else is competing for resources on the system. Profiling and benchmarking are the only reliable ways to dial it in. What works well on one setup might be suboptimal—or even harmful—on another.

## Benchmarking Impact

Buffered writes and reads consistently demonstrate significant performance gains under load. Benchmarks measuring system calls, memory allocations, and CPU usage typically show that buffered I/O operations are faster and more efficient than unbuffered counterparts. For example, writing one million lines to disk might exhibit up to an order-of-magnitude improvement using `bufio.Writer` compared to direct `os.File.Write()` calls. The more structured and bursty your I/O operations, the more substantial the benefits from buffering.

Show the benchmark file

```
package perf

import (
    "bufio"
    "io"
    "os"
    "strconv"
    "sync"
    "testing"
)

type Data struct {
    Value []byte
}

var dataPool = sync.Pool{
    New: func() any {
        return &Data{Value: make([]byte, 0, 32)}
    },
}

const N = 10000

func writeNotBuffered(w io.Writer, count int) {
    for i := 0; i < count; i++ {
        d := dataPool.Get().(*Data)
        d.Value = strconv.AppendInt(d.Value[:0], int64(i), 10)
        w.Write(d.Value)
        w.Write([]byte(":val\n"))
        dataPool.Put(d)
    }
}

func writeBuffered(w io.Writer, count int) {
    buf := bufio.NewWriterSize(w, 16*1024)
    for i := 0; i < count; i++ {
        d := dataPool.Get().(*Data)
        d.Value = strconv.AppendInt(d.Value[:0], int64(i), 10)
        buf.Write(d.Value)
        buf.Write([]byte(":val\n"))
        dataPool.Put(d)
    }
    buf.Flush()
}

func BenchmarkWriteNotBuffered(b *testing.B) {
    for b.Loop() {
        f, _ := os.CreateTemp("", "nobuf")
        writeNotBuffered(f, N)
        f.Close()
        os.Remove(f.Name())
    }
}

func BenchmarkWriteBuffered(b *testing.B) {
    for b.Loop() {
        f, _ := os.CreateTemp("", "buf")
        writeBuffered(f, N)
        f.Close()
        os.Remove(f.Name())
    }
}
```

Results:

| Benchmark | Iterations | Time per op (ns) | Bytes per op | Allocs per op |
| --- | --- | --- | --- | --- |
| BenchmarkWriteNotBuffered-14 | 49 | 23,672,792 | 53,773 | 10,007 |
| BenchmarkWriteBuffered-14 | 3241 | 379,703 | 70,127 | 10,008 |

## When To Buffer

Use buffering when:

- Performing frequent, small-sized I/O operations. Buffering groups small writes or reads into larger batches, which reduces the overhead of each individual operation.
- Reducing syscall overhead is crucial. Fewer syscalls mean lower context-switching costs and improved performance, especially in I/O-heavy applications.
- High throughput is more important than minimal latency. Buffered I/O can increase total data processed per second, even if it introduces slight delays in delivery.

Avoid buffering when:

- Immediate data availability and low latency are critical. Buffers introduce delays by design, which can be unacceptable in real-time or interactive systems.
- Buffering excessively might lead to uncontrolled memory usage. Without limits or proper flushing, buffers can grow large and put pressure on system memory.
