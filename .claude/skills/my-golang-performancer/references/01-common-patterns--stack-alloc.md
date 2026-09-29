Source: https://goperf.dev/01-common-patterns/stack-alloc/

# Stack Allocations and Escape Analysis

When writing performance-critical Go applications, one of the subtle but significant optimizations you can make is encouraging values to be allocated on the stack rather than the heap. Stack allocations are cheaper, faster, and garbage-free—but Go doesn't always put your variables there automatically. That decision is made by the Go compiler during **escape analysis**.

In this article, we’ll explore what escape analysis is, how to read the compiler’s escape diagnostics, what causes values to escape, and how to structure your code to minimize unnecessary heap allocations. We'll also benchmark different scenarios to show the real-world impact.

## What Is Escape Analysis?

Escape analysis is a static analysis performed by the Go compiler to determine whether a variable can be safely allocated on the stack or if it must be moved ("escape") to the heap.

### Why does it matter?

- **Stack allocations** are cheap: the memory is automatically freed when the function returns.
- **Heap allocations** are more expensive: they involve garbage collection overhead.

The compiler decides where to place each variable based on how it's used. If a variable can be guaranteed to not outlive its declaring function, it can stay on the stack. If not, it escapes to the heap.

### Example: Stack vs Heap

```
func allocate() *int {
    x := 42
    return &x // x escapes to the heap
}

func noEscape() int {
    x := 42
    return x // x stays on the stack
}
```

In `allocate`, `x` is returned as a pointer. Since the pointer escapes the function, the Go compiler places `x` on the heap. In `noEscape`, `x` is a plain value and doesn’t escape.

## How to View Escape Analysis Output

You can inspect escape analysis with the `-gcflags` compiler option:

```
go build -gcflags="-m" ./path/to/pkg
```

Or for a specific file:

```
go run -gcflags="-m" main.go
```

This will print lines like:

```
main.go:10:6: moved to heap: x
main.go:14:6: can inline noEscape
```

Look for messages like `moved to heap` to identify escape points.

## What Causes Variables to Escape?

Here are common scenarios that force heap allocation:

### Returning Pointers to Local Variables

```
func escape() *int {
    x := 10
    return &x // escapes
}
```

### Capturing Variables in Closures

```
func closureEscape() func() int {
    x := 5
    return func() int { return x } // x escapes
}
```

### Interface Conversions

When a value is stored in an interface, it may escape:

```
func toInterface(i int) interface{} {
    return i // escapes if type info needed at runtime
}
```

### Assignments to Global Variables or Struct Fields

```
var global *int

func assignGlobal() {
    x := 7
    global = &x // escapes
}
```

### Large Composite Literals

Go may allocate large structs or slices on the heap even if they don’t strictly escape.

```
func makeLargeSlice() []int {
    s := make([]int, 10000) // may escape due to size
    return s
}
```

## Benchmarking Stack vs Heap Allocations

Let’s run a benchmark to explore when heap allocations actually occur—and when they don’t, even if we return a pointer.

```
func StackAlloc() Data {
    return Data{1, 2, 3} // stays on stack
}

func HeapAlloc() *Data {
    return &Data{1, 2, 3} // escapes to heap
}

func BenchmarkStackAlloc(b *testing.B) {
    for b.Loop() {
        _ = StackAlloc()
    }
}

func BenchmarkHeapAlloc(b *testing.B) {
    for b.Loop() {
        _ = HeapAlloc()
    }
}
```

Benchmark Results

| Benchmark | Iterations | Time per op (ns) | Bytes per op | Allocs per op |
| --- | --- | --- | --- | --- |
| BenchmarkStackAlloc-14 | 1,000,000,000 | 0.2604 ns | 0 B | 0 |
| BenchmarkHeapAlloc-14 | 1,000,000,000 | 0.2692 ns | 0 B | 0 |

You might expect `HeapAlloc` to always allocate memory on the heap—but it doesn’t here. That’s because the compiler is smart: in this isolated benchmark, the pointer returned by `HeapAlloc` doesn’t escape the function in any meaningful way. The compiler can see it’s only used within the benchmark and short-lived, so it safely places it on the stack too.

### Forcing a Heap Allocation

```
var sink *Data

func HeapAllocEscape() {
    d := &Data{1, 2, 3}
    sink = d // d escapes to heap
}

func BenchmarkHeapAllocEscape(b *testing.B) {
    for b.Loop() {
        HeapAllocEscape()
    }
}
```

| Benchmark | Iterations | Time per op (ns) | Bytes per op | Allocs per op |
| --- | --- | --- | --- | --- |
| BenchmarkHeapAllocEscape-14 | 331,469,049 | 10.55 ns | 24 B | 1 |

As shown in `BenchmarkHeapAllocEscape`, assigning the pointer to a global variable causes a real heap escape. This introduces real overhead: a 40x slower call, a 24-byte allocation, and one garbage-collected object per call.

Show the benchmark file

```
package main

import "testing"

type Data struct {
    A, B, C int
}

// heap-alloc-start
func StackAlloc() Data {
    return Data{1, 2, 3} // stays on stack
}

func HeapAlloc() *Data {
    return &Data{1, 2, 3} // escapes to heap
}

func BenchmarkStackAlloc(b *testing.B) {
    for b.Loop() {
        _ = StackAlloc()
    }
}

func BenchmarkHeapAlloc(b *testing.B) {
    for b.Loop() {
        _ = HeapAlloc()
    }
}
// heap-alloc-end

// escape-start
var sink *Data

func HeapAllocEscape() {
    d := &Data{1, 2, 3}
    sink = d // d escapes to heap
}

func BenchmarkHeapAllocEscape(b *testing.B) {
    for b.Loop() {
        HeapAllocEscape()
    }
}
// escape-end
```

## When to Optimize for Stack Allocation

Not all escapes are worth preventing. Here’s when it makes sense to focus on stack allocation—and when it’s better to let values escape.

When to Avoid Escape

- In performance-critical paths. Reducing heap usage in tight loops or latency-sensitive code lowers GC pressure and speeds up execution.
- For short-lived, small objects. These can be efficiently stack-allocated without involving the garbage collector, reducing memory churn.
- When you control the full call chain. If the object stays within your code and you can restructure it to avoid escape, it’s often worth the small refactor.
- If profiling reveals GC bottlenecks. Escape analysis helps you target and shrink memory-heavy allocations identified in real-world traces.

When It’s Fine to Let Values Escape

- When returning values from constructors or factories. Returning a pointer from `NewThing()` is idiomatic Go—even if it causes an escape, it improves clarity and usability.
- When objects must outlive the function. If you're storing data in a global, sending to a goroutine, or saving it in a struct, escaping is necessary and correct.
- When allocation size is small and infrequent. If the heap allocation isn’t in a hot path, the benefit of avoiding it is often negligible.
- When preventing escape hurts readability. Writing awkward code to keep everything on the stack can reduce maintainability for a micro-optimization that won’t matter.
