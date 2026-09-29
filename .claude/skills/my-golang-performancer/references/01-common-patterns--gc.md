Source: https://goperf.dev/01-common-patterns/gc/

# Memory Efficiency: Mastering Go’s Garbage Collector

Memory management in Go is automated—but it’s not invisible. Every allocation you make contributes to GC workload. The more frequently objects are created and discarded, the more work the runtime has to do reclaiming memory.

This becomes especially relevant in systems prioritizing low latency, predictable resource usage, or high throughput. Tuning your allocation patterns and leveraging newer features like weak references can help reduce pressure on the GC without adding complexity to your code.

## How Go's Garbage Collector Works

Info

Highly encourage you to read the official [A Guide to the Go Garbage Collector](https://go.dev/doc/gc-guide)! The document provides a detailed description of multiple Go's GC internals.

Go uses a **non-generational, concurrent, tri-color mark-and-sweep** garbage collector. Here's what that means in practice and how it's implemented.

### Non-generational

Many modern GCs, like those in the JVM or .NET CLR, divide memory into _generations_ (young and old) under the assumption that most objects die young. These collectors focus on the young generation, which leads to shorter collection cycles.

Go’s GC takes a different approach. It treats all objects equally—no generational segmentation—not because generational GC conflicts with short pause times or concurrent scanning, but because it hasn’t shown clear, consistent benefits in real-world Go programs with the designs tried so far. This choice avoids the complexity of promotion logic and specialized memory regions. While it can mean scanning more objects overall, this cost is mitigated by concurrent execution and efficient write barriers.

### Concurrent

Go’s GC runs concurrently with your application, which means it does most of its work without stopping the world. Concurrency is implemented using multiple phases that interleave with normal program execution:

Even though Go’s garbage collector is mostly concurrent, it still requires brief Stop-The-World (STW) pauses at several points to maintain correctness. These pauses are kept extremely short—typically under 100 microseconds—even with large heaps and hundreds of goroutines.

STW is essential for ensuring that memory structures are not mutated while the GC analyzes them. In most applications, these pauses are imperceptible. However, even sub-millisecond pauses in latency-sensitive systems can be significant—so understanding and monitoring STW behavior becomes important when optimizing for tail latencies or jitter.

- **STW Start Phase:** The application is briefly paused to initiate GC. The runtime scans stacks, globals, and root objects.
- **Concurrent Mark Phase:** The garbage collector traverses the heap, marking all reachable objects while the program continues running. This is the heaviest phase in terms of work but runs concurrently to avoid long stop-the-world pauses.
- **STW Mark Termination:** Once marking is mostly complete, the GC briefly pauses the program to finish any remaining work and ensure the heap is in a consistent state before sweeping begins. This pause is typically very short—measured in microseconds.
- **Concurrent Sweep Phase:** The GC reclaims memory from unreachable (white) objects and returns it to the heap for reuse, all while your program continues running.

Write barriers ensure correctness while the application mutates objects during concurrent marking. These barriers help track references created or modified mid-scan so the GC doesn’t miss them.

### Tri-color Mark and Sweep

The tri-color algorithm breaks the heap into three working sets during garbage collection:

- **White:** Objects that haven’t been reached—if they stay white, they’ll be collected.
- **Grey:** Objects that have been discovered (i.e., marked as reachable) but haven’t had their references scanned yet.
- **Black:** Objects that are both reachable and fully scanned—they’re retained and don’t need further processing.

Garbage collection starts by marking all root objects (stack, globals, etc.) grey. It then walks the grey set: for each object, it scans its fields. Any referenced objects that are still white are added to the grey set. Once an object’s references are fully processed, it’s marked black.

When no grey objects remain, anything still white is unreachable and gets cleaned up during the sweep phase. This model ensures that no live object is accidentally collected—even if references change mid-scan—thanks to Go’s write barriers that maintain the algorithm’s core invariants.

A key optimization is **incremental marking**: Go spreads out GC work to avoid long pauses, supported by precise stack scanning and conservative write barriers. The use of concurrent sweeping further reduces latency, allowing memory to be reclaimed without halting execution.

This design gives Go a GC that’s safe, fast, and friendly to server workloads with large heaps and many cores.

## GC Tuning: GOGC

Go’s garbage collector is tuned to deliver good performance without manual configuration. The default `GOGC` setting typically strikes the right balance between memory consumption and CPU effort, adapting well across a wide range of workloads. In most cases, manually tweaking it offers little benefit—and in many, it actually makes things worse by increasing either pause times or memory pressure. Unless you’ve profiled a specific bottleneck and understand the trade-offs, it’s usually best to leave `GOGC` alone.

That said, there are specific cases where tuning `GOGC` can yield significant gains. For example, [Uber implemented dynamic GC tuning](https://www.uber.com/en-GB/blog/how-we-saved-70k-cores-across-30-mission-critical-services/) across their Go services to reduce CPU usage and saved tens of thousands of cores in the process. Their approach relied on profiling, metric collection, and automation to safely adjust GC behavior based on actual memory pressure and workload characteristics.

Another unusual case is from Cloudflare. They [profiled a high-concurrency cryptographic workload](https://blog.cloudflare.com/go-dont-collect-my-garbage/) and found that Go’s GC became a bottleneck as goroutines increased. Their application produced minimal garbage, yet GC overhead grew with concurrency. By tuning GOGC to a much higher value—specifically 11300—they significantly reduced GC frequency and improved throughput, achieving over 22× performance gains compared to the single-core baseline. This case highlights how allowing more heap growth in CPU-bound and low-allocation scenarios can yield major improvements.

So, if you decide to tune the garbage collector, be methodical:

- Always profile first. Use tools like `pprof` to confirm that GC activity is a bottleneck.
- Change settings incrementally. For example, increasing `GOGC` from 100 to 150 means the GC will run less frequently, using less CPU but more memory.
- Verify impact. After tuning, validate with profiling data that the change had a positive effect. Without that confirmation, it's easy to make things worse.

```
GOGC=100  # Default: GC runs when heap grows 100% since last collection
GOGC=off  # Disables GC (use only in special cases like short-lived CLI tools)
```

### Memory Limiting with `GOMEMLIMIT`

In addition to `GOGC`, Go provides `GOMEMLIMIT`—a soft memory limit that caps the total heap size the runtime will try to stay under. This allows you to explicitly control memory growth, especially useful in environments like containers or systems with strict memory budgets.

Why is this helpful? In containerized environments (like Kubernetes), memory limits are typically enforced at the OS or orchestrator level. If your application exceeds its memory quota, the OOM killer may abruptly terminate the container. Go's GC isn't aware of those limits by default.

Setting a `GOMEMLIMIT` helps prevent this. For example, if your container has a 512MiB memory limit, you might set:

```
GOMEMLIMIT=400MiB
```

This buffer gives the Go runtime room to act before reaching the hard system-imposed memory cap. It allows the garbage collector to become more aggressive as total memory usage grows, reducing the chances of the process being killed due to an out-of-memory condition. It also leaves space for non-heap allocations—like goroutine stacks, OS threads, and other internal runtime structures—which don’t count toward heap size but still consume real memory.

You can also set the limit programmatically:

```
import "runtime/debug"

debug.SetMemoryLimit(2 << 30) // 2 GiB
```

The GC will become more aggressive as heap usage nears the limit, which can increase CPU load. Be careful not to set the limit too low—especially if your application maintains a large live set of objects—or you may trigger excessive GC cycles.

While `GOGC` controls how frequently the GC runs based on heap growth, `GOMEMLIMIT` constrains the heap size itself. The two can be combined for more precise control:

```
GOGC=100 GOMEMLIMIT=4GiB ./your-service
```

This tells the GC to operate with the default growth ratio and to start collecting sooner if heap usage nears 4 GiB.

### GOMEMLIMIT=X and GOGC=off configuration

In scenarios where memory availability is fixed and predictable—such as within containers or VMs, you can use these two variables together:

- `GOMEMLIMIT=X` tells the runtime to aim for a specific memory ceiling. For example, `GOMEMLIMIT=2GiB` will trigger garbage collection when total memory usage nears 2 GiB.
- `GOGC=off` disables the default GC pacing algorithm, so garbage collection only runs when the memory limit is hit.

This configuration maximizes memory usage efficiency and avoids the overhead of frequent GC cycles. It's especially effective in high-throughput or latency-sensitive systems where predictable memory usage matters.

**Example:**

```
GOMEMLIMIT=2GiB GOGC=off ./my-app
```

With this setup, memory usage grows freely until the 2 GiB threshold is reached. At that point, Go performs a full garbage collection pass.

Warning

- Always benchmark with your real workload. Disabling automatic GC can backfire if your application produces a lot of short-lived allocations.
- Monitor memory pressure and GC pause times using `runtime.ReadMemStats` or `pprof`.
- This approach works best when your memory usage patterns are well understood and stable.

## Practical Strategies for Reducing GC Pressure

### Prefer Stack Allocation

Go allocates variables on the stack whenever possible. Avoid escaping variables to the heap:

```
// BAD: returns pointer to heap-allocated struct
func newUser(name string) *User {
    return &User{Name: name}  // escapes to heap
}

// BETTER: use value types if pointer is unnecessary
func printUser(u User) {
    fmt.Println(u.Name)
}
```

Use `go build -gcflags="-m"` to view escape analysis diagnostics. See [Stack Allocations and Escape Analysis](https://goperf.dev/01-common-patterns/stack-alloc/) for more details.

### Use sync.Pool for Short-Lived Objects

`sync.Pool` is ideal for temporary, reusable allocations that are expensive to GC.

```
var bufPool = sync.Pool{
    New: func() any { return new(bytes.Buffer) },
}

func handler(w http.ResponseWriter, r *http.Request) {
    buf := bufPool.Get().(*bytes.Buffer)
    buf.Reset()
    defer bufPool.Put(buf)

    // Use buf...
}
```

See [Object Pooling](https://goperf.dev/01-common-patterns/object-pooling/) for more details.

### Batch Allocations

Group allocations into fewer objects to reduce GC pressure.

```
// Instead of allocating many small structs, allocate a slice of structs
users := make([]User, 0, 1000)  // single large allocation
```

See [Memory Preallocation](https://goperf.dev/01-common-patterns/mem-prealloc/) for more details.

## Weak References in Go

Go 1.24 added the `weak` package, providing a standardized way to create weak references—pointers that don’t keep their target objects alive. In garbage-collected systems like Go, strong references extend an object’s lifetime: as long as something points to it, it won’t be collected. That’s usually what you want, but in structures like caches, deduplication maps, or object graphs, this can lead to memory staying alive much longer than intended. Weak references solve that by allowing you to refer to an object without blocking the GC from reclaiming it when nothing else is using it.

A weak reference, by contrast, tells the garbage collector: “you can collect this object if nothing else is strongly referencing it.” This pattern is important for building memory-sensitive data structures that should not interfere with garbage collection.

```
package main

import (
    "fmt"
    "runtime"
    "weak"
)

type Data struct {
    Value string
}

func main() {
    data := &Data{Value: "Important"}
    wp := weak.Make(data) // create weak pointer

    fmt.Println("Original:", wp.Value().Value)

    data = nil // remove strong reference
    runtime.GC()

    if v := wp.Value(); v != nil {
        fmt.Println("Still alive:", v.Value)
    } else {
        fmt.Println("Data has been collected")
    }
}
```

```
Original: Important
Data has been collected
```

In this example, `wp` holds a weak reference to a `Data` object. After the strong reference (`data`) goes out of scope and the garbage collector runs, the `Data` may be collected—at which point `wp.Value()` will return nil. This pattern is especially useful in memory-sensitive contexts like caches or canonicalization maps, where you want to avoid artificially extending object lifetimes. Always check the result of `Value()` before using it, since the target may have been reclaimed.

## Benchmarking Impact

It's tempting to rely on synthetic benchmarks to evaluate the performance of Go's garbage collector, but generic benchmarks rarely capture the nuances of real-world workloads. Memory behavior is highly dependent on allocation patterns, object lifetimes, concurrency, and how frequently short-lived versus long-lived data structures are used.

For example, the impact of GC in a CPU-bound microservice that maintains large in-memory indexes will differ dramatically from an I/O-heavy API server with minimal heap usage. As such, tuning decisions should always be informed by your application's profiling data.

We cover targeted use cases and their GC performance trade-offs in more focused articles:

- [Object Pooling](https://goperf.dev/01-common-patterns/object-pooling/): Reducing allocation churn using `sync.Pool`
- [Stack Allocations and Escape Analysis](https://goperf.dev/01-common-patterns/stack-alloc/): Minimizing heap usage by keeping values on the stack
- [Memory Preallocation](https://goperf.dev/01-common-patterns/mem-prealloc/): Avoiding unnecessary growth of slices and maps

When applied to the right context, these techniques can make a measurable difference, but they don’t lend themselves to one-size-fits-all benchmarks.
