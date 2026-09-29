Source: https://goperf.dev/01-common-patterns/worker-pool/

# Goroutine Worker Pools in Go

Go’s concurrency model makes it deceptively easy to spin up thousands of goroutines—but that ease can come at a cost. Each goroutine starts small, but under load, unbounded concurrency can cause memory usage to spike, context switches to pile up, and overall performance to become unpredictable.

A worker pool helps apply backpressure by limiting the number of active goroutines. Instead of spawning one per task, a fixed pool handles work in controlled parallelism—keeping memory usage predictable and avoiding overload. This makes it easier to maintain steady performance even as demand scales.

## Why Worker Pools Matter

While launching a goroutine for every task is idiomatic and often effective, doing so at scale comes with trade-offs. Each goroutine requires stack space and introduces scheduling overhead. Performance can degrade sharply when the number of active goroutines grows, especially in systems handling unbounded input like HTTP requests, jobs from a queue, or tasks from a channel.

A worker pool maintains a fixed number of goroutines that pull tasks from a shared job queue. This creates a backpressure mechanism, ensuring the system never processes more work concurrently than it can handle. Worker pools are particularly valuable when the cost of each task is predictable, and the overall system throughput needs to be stable.

## Basic Worker Pool Implementation

Here’s a minimal implementation of a worker pool:

```
func worker(id int, jobs <-chan int, results chan<- [32]byte) {
    for j := range jobs {
        results <- doWork(j)
    }
}

func doWork(n int) [32]byte {
    data := []byte(fmt.Sprintf("payload-%d", n))
    return sha256.Sum256(data)                  // 
}

func main() {
    jobs := make(chan int, 100)
    results := make(chan [32]byte, 100)

    for w := 1; w <= 5; w++ {
        go worker(w, jobs, results)
    }

    for j := 1; j <= 10; j++ {
        jobs <- j
    }
    close(jobs)

    for a := 1; a <= 10; a++ {
        <-results
    }
}
```

In this example, five workers pull from the `jobs` channel and push results to the `results` channel. The worker pool limits concurrency to five tasks at a time, regardless of how many tasks are sent.

### Worker Count and CPU Cores

The optimal number of workers in a pool is closely tied to the number of CPU cores, which you can obtain in Go using `runtime.NumCPU()` or `runtime.GOMAXPROCS(0)`. For CPU-bound tasks—where each worker consumes substantial CPU time—you generally want the number of workers to be equal to or slightly less than the number of logical CPU cores. This ensures maximum core utilization without excessive overhead.

If your tasks are I/O-bound (e.g., network calls, disk I/O, database queries), the pool size can be larger than the number of cores. This is because workers will spend much of their time blocked, allowing others to run. In contrast, CPU-heavy workloads benefit from a smaller, tightly bounded pool that avoids contention and context switching.

### Why Too Many Workers Hurts Performance

Adding more workers can seem like a straightforward way to boost throughput, but the benefits taper off quickly past a certain point. Once you exceed the system’s optimal level of concurrency, performance often degrades instead of improving.

- Scheduler contention increases as the Go runtime juggles more runnable goroutines than it has logical CPUs to run them.
- Context switching grows more frequent, burning CPU cycles without doing real work.
- Memory pressure rises because each goroutine holds its own stack, even when idle.
- Cache thrashing becomes more likely as goroutines bounce across cores, disrupting locality and degrading CPU cache performance.

The result: higher latency, increased GC activity, and reduced throughput—the exact opposite of what a properly tuned worker pool is supposed to deliver.

## Benchmarking Impact

Worker pools shine in scenarios where the workload is CPU-bound or where concurrency must be capped to avoid saturating a shared resource (e.g., database connections or file descriptors). Benchmarks comparing unbounded goroutine launches vs. worker pools typically show:

- Lower peak memory usage
- More stable response times under load
- Improved CPU cache locality

Show the benchmark file

```
package perf

import (
    // "log"
    "fmt"
    // "os"
    "runtime"
    "sync"
    "testing"
    "crypto/sha256"
)

const (
    numJobs     = 10000
    workerCount = 10
)

func doWork(n int) [32]byte {
    data := []byte(fmt.Sprintf("payload-%d", n))
    return sha256.Sum256(data)
}

func BenchmarkUnboundedGoroutines(b *testing.B) {
    for b.Loop() {
        var wg sync.WaitGroup
        wg.Add(numJobs)

        for j := 0; j < numJobs; j++ {
            go func(job int) {
                _ = doWork(job)
                wg.Done()
            }(j)
        }
        wg.Wait()
    }
}

func worker(jobs <-chan int, wg *sync.WaitGroup) {
    for job := range jobs {
        _ = doWork(job)
        wg.Done()
    }
}

func BenchmarkWorkerPool(b *testing.B) {
    for b.Loop() {
        var wg sync.WaitGroup
        wg.Add(numJobs)

        jobs := make(chan int, numJobs)
        for w := 0; w < workerCount; w++ {
            go worker(jobs, &wg)
        }

        for j := 0; j < numJobs; j++ {
            jobs <- j
        }

        close(jobs)
        wg.Wait()
    }
}
```

Results:

| Benchmark | Iterations | Time per op (ns) | Bytes per op | Allocs per op |
| --- | --- | --- | --- | --- |
| BenchmarkUnboundedGoroutines-14 | 2,274 | 2,499,213 ns | 639,350 | 39,754 |
| BenchmarkWorkerPool-14 | 3,325 | 1,791,772 ns | 320,707 | 19,762 |

In our benchmark, each task performed a CPU-intensive operation (e.g., cryptographic hashing, math, or serialization). With `workerCount = 10` on an Apple M3 Max machine, the worker pool outperformed the unbounded goroutine model by a significant margin, using fewer resources and completing work faster. Increasing the worker count beyond the number of available cores led to worse performance due to contention.

## When To Use Worker Pools

Use a goroutine worker pool when:

- The workload is unbounded or high volume. A pool prevents uncontrolled goroutine growth, which can lead to memory exhaustion, GC pressure, and unpredictable performance.
- Unbounded concurrency risks resource saturation. Capping the number of concurrent workers helps avoid overwhelming the CPU, network, database, or disk I/O—especially under load.
- You need predictable parallelism for stability. Limiting concurrency smooths out performance spikes and keeps system behavior consistent, even during traffic surges.
- Tasks are relatively uniform and queue-friendly. When task cost is consistent, a fixed pool size provides efficient scheduling with minimal overhead, ensuring good throughput without complex coordination.

Avoid a worker pool when:

- Each task must be processed immediately with minimal latency. Queuing in a worker pool introduces delay. For latency-critical tasks, direct goroutine spawning avoids the scheduling overhead.
- You can rely on Go's scheduler for natural load balancing in low-load scenarios. In light workloads, the overhead of managing a pool may outweigh its benefits. Go’s scheduler can often handle lightweight parallelism efficiently on its own.
- Workload volume is small and bounded. Spinning up goroutines directly keeps code simpler for limited, predictable workloads without risking uncontrolled growth.
