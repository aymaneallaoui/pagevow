Source: https://goperf.dev/01-common-patterns/

# Common Go Patterns for Performance

Optimizing Go applications requires understanding common patterns that help reduce latency, improve memory efficiency, and enhance concurrency. This guide organizes 15 key techniques into four practical categories.

---

## Memory Management & Efficiency

These strategies help reduce memory churn, avoid excessive allocations, and improve cache behavior.

- [Object Pooling](https://goperf.dev/01-common-patterns/object-pooling/) 
 Reuse objects to reduce GC pressure and allocation overhead.

- [Memory Preallocation](https://goperf.dev/01-common-patterns/mem-prealloc/) 
 Allocate slices and maps with capacity upfront to avoid costly resizes.

- [Struct Field Alignment](https://goperf.dev/01-common-patterns/fields-alignment/) 
 Optimize memory layout to minimize padding and improve locality.

- [Avoiding Interface Boxing](https://goperf.dev/01-common-patterns/interface-boxing/) 
 Prevent hidden allocations by avoiding unnecessary interface conversions.

- [Zero-Copy Techniques](https://goperf.dev/01-common-patterns/zero-copy/) 
 Minimize data copying with slicing and buffer tricks.

- [Memory Efficiency and Go’s Garbage Collector](https://goperf.dev/01-common-patterns/gc/) 
 Reduce GC overhead by minimizing heap usage and reusing memory.

- [Stack Allocations and Escape Analysis](https://goperf.dev/01-common-patterns/stack-alloc/) 
 Use escape analysis to help values stay on the stack where possible.

---

## Concurrency and Synchronization

Manage goroutines, shared resources, and coordination efficiently.

- [Goroutine Worker Pools](https://goperf.dev/01-common-patterns/worker-pool/) 
 Control concurrency with a fixed-size pool to limit resource usage.

- [Atomic Operations and Synchronization Primitives](https://goperf.dev/01-common-patterns/atomic-ops/) 
 Use atomic operations or lightweight locks to manage shared state.

- [Lazy Initialization (`sync.Once`)](https://goperf.dev/01-common-patterns/lazy-init/) 
 Delay expensive setup logic until it's actually needed.

- [Immutable Data Sharing](https://goperf.dev/01-common-patterns/immutable-data/) 
 Share data safely between goroutines without locks by making it immutable.

- [Efficient Context Management](https://goperf.dev/01-common-patterns/context/) 
 Use `context` to propagate timeouts and cancel signals across goroutines.

---

## I/O Optimization and Throughput

Reduce system call overhead and increase data throughput for I/O-heavy workloads.

- [Efficient Buffering](https://goperf.dev/01-common-patterns/buffered-io/) 
 Use buffered readers/writers to minimize I/O calls.

- [Batching Operations](https://goperf.dev/01-common-patterns/batching-ops/) 
 Combine multiple small operations to reduce round trips and improve throughput.

---

## Compiler-Level Optimization and Tuning

Tap into Go’s compiler and linker to further optimize your application.

- [Leveraging Compiler Optimization Flags](https://goperf.dev/01-common-patterns/comp-flags/) 
 Use build flags like `-gcflags` and `-ldflags` for performance tuning.

- [Stack Allocations and Escape Analysis](https://goperf.dev/01-common-patterns/stack-alloc/) 
 Analyze which values escape to the heap to help the compiler optimize memory placement.
