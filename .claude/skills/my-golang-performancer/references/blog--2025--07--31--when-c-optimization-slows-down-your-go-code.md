Source: https://goperf.dev/blog/2025/07/31/when-c-optimization-slows-down-your-go-code/

# When C++ Optimization Slows Down Your Go Code

When you have years of C++ experience, you definitely obtain some habits. These habits are good for C++, but could cause you some surprises in Go. In C++, you usually preallocate everything, avoiding unnecessary allocations, caching values aggressively, and always thinking of CPU cache misses. So when I rewrote a simple algorithm in Go—finding the number of days until the next warmer temperature—I reached for the same tricks. But this time, they backfired.

Here’s how applying familiar C++ optimizations ended up making my Go code slower and heavier.

## The C++ Context

The original problem: for each day, figure out how many days pass until a warmer temperature appears. A classic use case for a monotonic stack. Here's the performance data [from a C++ implementation](https://github.com/astavonin/perf-tests/blob/main/daily-temps/cpp/daily_temperatures.cpp):

dailyTemperatures, basic implementation

```
std::vector<int> dailyTemperatures( const std::vector<int> &temperatures )
{
    std::vector<int> result( temperatures.size(), 0 );
    std::stack<int>  s;
    for( int i = 0; i < temperatures.size(); ++i ) {
        while( !s.empty() && temperatures[i] > temperatures[s.top()] ) {
            int prev = s.top();
            s.pop();
            result[prev] = i - prev;
        }
        s.push( i );
    }
    return result;
}
```
dailyTemperatures, optimized implementation

```
std::vector<int> dailyTemperaturesOpt( const std::vector<int> &temperatures )
{
    std::vector<int> res( temperatures.size(), 0 );
    std::vector<int> track; // 
    track.reserve( temperatures.size() ); // 

    for( int i = 0; i < temperatures.size(); ++i ) {
        int currTemp = temperatures[i]; // 
        while( !track.empty() && currTemp > temperatures[track.back()] ) {
            int prev = track.back();
            track.pop_back();
            res[prev] = i - prev;
        }
        track.push_back( i );
    }
    return res;
}
```

| Benchmark | Time per op (ns) |
| --- | --- |
| BM\_DailyTemperatures/100000 | 206,340 |
| BM\_DailyTemperaturesOpt/100000 | 115,490 |

Standard C++ optimization tactics can cut the runtime almost in half. But even if something works in C++ pretty well, it does not mean that the same approach will not make your Go code slower.

## Translating to Go

Here’s what a clean idiomatic Go version looks like:

```
func DailyTemperatures(temperatures []int) []int {
    result := make([]int, len(temperatures))
    var stack []int

    for i, temp := range temperatures {
        for len(stack) > 0 && temp > temperatures[stack[len(stack)-1]] {
            prevIndex := stack[len(stack)-1]
            stack = stack[:len(stack)-1]
            result[prevIndex] = i - prevIndex
        }
        stack = append(stack, i)
    }

    return result
}
```

Pretty typical: no preallocation, straightforward stack growth via append.

I rewrote this with “optimizations”: preallocate the stack slice, replace variables early, and reduce slice bounds checks. Classic C++-style low-level thinking.

```
func DailyTemperaturesOpt(temperatures []int) []int {
    n := len(temperatures)
    result := make([]int, n)
    stack := make([]int, 0, n/4) // 

    for i := 0; i < n; i++ {
        curr := temperatures[i] // 
        for len(stack) > 0 && curr > temperatures[stack[len(stack)-1]] {
            prev := stack[len(stack)-1]
            stack = stack[:len(stack)-1]
            result[prev] = i - prev
        }
        stack = append(stack, i)
    }
    return result
}
```

And the Result? It got worse.

| Benchmark | Time per op (ns) | Bytes per op | Allocs per op |
| --- | --- | --- | --- |
| BenchmarkDailyTemperatures/Baseline-14 | 174,419 | 862,847 | 15 |
| BenchmarkDailyTemperatures/Optimized-14 | 175,021 | 1,007,620 | 2 |

Same logic, but now slower and heavier. One fewer allocation, but an extra 150KB of memory usage. Why? Because in Go, allocating a 100,000-capacity slice (even if you barely use it) is expensive. The runtime doesn’t treat that lightly.

## Why Go Behaves Differently

The runtime is more opinionated. Memory is GC-managed. There’s no real benefit to preallocating more than you need, especially if your code doesn’t end up using it. `append()` is cheap, and in many cases more efficient than second-guessing the allocator.

On top of that, Go’s escape analysis doesn’t work the way C++’s stack allocation does. What you think is local might end up on the heap, just because of one indirect reference.

Key Takeaways

- Preallocation helps in C++ because memory layout and growth are under your control. In Go, the runtime handles it differently.
- Trust the idioms of the language. If you’re writing Go, let Go be Go.
- Measure everything. Some optimizations only look good on paper—or in other languages.

---

I still write C++. I still optimize memory, inlining, and stack frames. But when I’m in Go, I’ve learned to lean into the model that Go is designed for. Sometimes performance comes from understanding how much less you need to do—not how much you can tweak.
