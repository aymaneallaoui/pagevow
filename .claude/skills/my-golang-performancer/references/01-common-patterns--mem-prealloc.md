Source: https://goperf.dev/01-common-patterns/mem-prealloc/

# Memory Preallocation

Memory preallocation is a simple but effective way to improve performance in Go programs that work with slices or maps that grow over time. Instead of letting the runtime resize these structures as they fill up—often at unpredictable points—you allocate the space you need upfront. This avoids the cost of repeated allocations, internal copying, and extra GC pressure as intermediate objects are created and discarded.

In high-throughput or latency-sensitive systems, preallocating memory makes execution more predictable and helps avoid performance cliffs that show up under load. If the workload size is known or can be reasonably estimated, there’s no reason to let the allocator do the guessing.

## Why Preallocation Matters

Go’s slices and maps grow automatically as new elements are added, but that convenience comes with a cost. When capacity is exceeded, the runtime allocates a larger backing array or hash table and copies the existing data over. This reallocation adds memory pressure, burns CPU cycles, and can stall tight loops in high-throughput paths. In performance-critical code—especially where the size is known or can be estimated—frequent resizing is unnecessary overhead. Preallocating avoids these penalties by giving the runtime enough room to work without interruption.

Go uses a hybrid growth strategy for slices to balance speed and memory efficiency. Early on, capacities double with each expansion—2, 4, 8, 16—minimizing the number of allocations. But once a slice exceeds around 1024 elements, the growth rate slows to roughly 25%. So instead of jumping from 1024 to 2048, the next allocation might grow to about 1280.

This shift reduces memory waste on large slices but increases the frequency of allocations if the final size is known but not preallocated. In those cases, using make(\[\]T, 0, expectedSize) is the more efficient choice—it avoids repeated resizing and cuts down on unnecessary copying.

```
s := make([]int, 0)
for i := 0; i < 10_000; i++ {
    s = append(s, i)
    fmt.Printf("Len: %d, Cap: %d\n", len(s), cap(s))
}
```

Output illustrating typical growth:

```
Len: 1, Cap: 1
Len: 2, Cap: 2
Len: 3, Cap: 4
Len: 5, Cap: 8
...
Len: 1024, Cap: 1024
Len: 1025, Cap: 1280
```

## Practical Preallocation Examples

### Slice Preallocation

Without preallocation, each append operation might trigger new allocations:

```
// Inefficient
var result []int
for i := 0; i < 10000; i++ {
    result = append(result, i)
}
```

This pattern causes Go to allocate larger underlying arrays repeatedly as the slice grows, resulting in memory copying and GC pressure. We can avoid that by using `make` with a specified capacity:

```
// Efficient
result := make([]int, 0, 10000)
for i := 0; i < 10000; i++ {
    result = append(result, i)
}
```

If it is known that the slice will be fully populated, we can be even more efficient by avoiding bounds checks:

```
// Efficient
result := make([]int, 10000)
for i := range result {
    result[i] = i
}
```

### Map Preallocation

Maps grow similarly. By default, Go doesn’t know how many elements you’ll add, so it resizes the underlying structure as needed.

```
// Inefficient
m := make(map[int]string)
for i := 0; i < 10000; i++ {
    m[i] = fmt.Sprintf("val-%d", i)
}
```

Starting with Go 1.11, you can preallocate `map` capacity too:

```
// Efficient
m := make(map[int]string, 10000)
for i := 0; i < 10000; i++ {
    m[i] = fmt.Sprintf("val-%d", i)
}
```

This helps the runtime allocate enough internal storage upfront, avoiding rehashing and resizing costs.

## Benchmarking Impact

Here’s a simple benchmark comparing appending to a preallocated slice vs. a zero-capacity slice:

Show the benchmark file

```
package perf

import (
    "testing"
)

func BenchmarkAppendNoPrealloc(b *testing.B) {
    for b.Loop() {
        var s []int
        for j := 0; j < 10000; j++ {
            s = append(s, j)
        }
    }
}

func BenchmarkAppendWithPrealloc(b *testing.B) {
    for b.Loop() {
        s := make([]int, 0, 10000)
        for j := 0; j < 10000; j++ {
            s = append(s, j)
        }
    }
}
```

You’ll typically observe that preallocation reduces allocations to a single one per operation and significantly improves throughput.

| Benchmark | Iterations | Time per op (ns) | Bytes per op | Allocs per op |
| --- | --- | --- | --- | --- |
| BenchmarkAppendNoPrealloc-14 | 41,727 | 28,539 | 357,626 | 19 |
| BenchmarkAppendWithPrealloc-14 | 170,154 | 7,093 | 81,920 | 1 |

## When To Preallocate

Preallocate when:

- The number of elements in slices or maps is known or reasonably predictable. Allocating memory up front avoids the cost of repeated resizing as the data structure grows.
- Your application involves tight loops or high-throughput data processing. Preallocation reduces per-iteration overhead and helps maintain steady performance under load.
- Minimizing garbage collection overhead is crucial for your application's performance. Fewer allocations mean less work for the garbage collector, resulting in lower latency and more consistent behavior.

Avoid preallocation when:

- The data size is highly variable and unpredictable. If input sizes fluctuate widely, any fixed-size preallocation risks being either too small (leading to reallocations) or too large (wasting memory).
- Over-allocation risks significant memory waste. Reserving more memory than needed increases your application’s footprint and can negatively impact cache locality or trigger unnecessary GC activity.
- You’re prematurely optimizing. Always verify with profiling. Preallocation is effective, but only when it addresses a real bottleneck or allocation hotspot in your workload.
