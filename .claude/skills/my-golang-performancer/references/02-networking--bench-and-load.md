Source: https://goperf.dev/02-networking/bench-and-load/

# Benchmarking and Load Testing for Networked Go Apps

Before you reach for a mutex-free queue or tune your goroutine pool, step back. Optimization without a baseline is just guesswork. In Go applications, performance tuning starts with understanding how your system behaves under pressure, which means benchmarking it under load.

Load testing isn't just about pushing requests until things break. It's about simulating realistic usage patterns to extract measurable, repeatable data. That data anchors every optimization that follows.

## Test App: Simulating Fast/Slow Paths and GC pressure

To benchmark meaningfully, we need endpoints that reflect different workload characteristics.

Show the benchmarking app

```
package main

// pprof-start
import (
// pprof-end
    "flag"
    "fmt"
    "log"
    "math/rand/v2"
    "net/http"
// pprof-start
    _ "net/http/pprof"
// pprof-end
    "os"
    "os/signal"
    "time"
// pprof-start
)
// pprof-end

var (
    fastDelay   = flag.Duration("fast-delay", 0, "Fixed delay for fast handler (if any)")
    slowMin     = flag.Duration("slow-min", 1*time.Millisecond, "Minimum delay for slow handler")
    slowMax     = flag.Duration("slow-max", 300*time.Millisecond, "Maximum delay for slow handler")
    gcMinAlloc  = flag.Int("gc-min-alloc", 50, "Minimum number of allocations in GC heavy handler")
    gcMaxAlloc  = flag.Int("gc-max-alloc", 1000, "Maximum number of allocations in GC heavy handler")
)

func randRange(min, max int) int {
    return rand.IntN(max-min) + min
}

func fastHandler(w http.ResponseWriter, r *http.Request) {
    if *fastDelay > 0 {
        time.Sleep(*fastDelay)
    }
    fmt.Fprintln(w, "fast response")
}

func slowHandler(w http.ResponseWriter, r *http.Request) {
    delayRange := int((*slowMax - *slowMin) / time.Millisecond)
    delay := time.Duration(randRange(1, delayRange)) * time.Millisecond
    time.Sleep(delay)
    fmt.Fprintf(w, "slow response with delay %d ms\n", delay.Milliseconds())
}

// heavy-start
var longLivedData [][]byte

func gcHeavyHandler(w http.ResponseWriter, r *http.Request) {
    numAllocs := randRange(*gcMinAlloc, *gcMaxAlloc)
    var data [][]byte
    for i := 0; i < numAllocs; i++ {
        // Allocate 10KB slices. Occasionally retain a reference to simulate long-lived objects.
        b := make([]byte, 1024*10)
        data = append(data, b)
        if i%100 == 0 { // every 100 allocations, keep the data alive
            longLivedData = append(longLivedData, b)
        }
    }
    fmt.Fprintf(w, "allocated %d KB\n", len(data)*10)
}
// heavy-end

func main() {
    flag.Parse()

    http.HandleFunc("/fast", fastHandler)
    http.HandleFunc("/slow", slowHandler)
    http.HandleFunc("/gc", gcHeavyHandler)

// pprof-start
// ...

    // Start pprof in a separate goroutine.
    go func() {
        log.Println("pprof listening on :6060")
        if err := http.ListenAndServe("localhost:6060", nil); err != nil {
            log.Fatalf("pprof server error: %v", err)
        }
    }()
// pprof-end

    // Create a server to allow for graceful shutdown.
    server := &http.Server{Addr: ":8080"}

    go func() {
        log.Println("HTTP server listening on :8080")
        if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("HTTP server error: %v", err)
        }
    }()

    // Graceful shutdown on interrupt signal.
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, os.Interrupt)
    <-sigCh
    log.Println("Shutting down server...")
    if err := server.Shutdown(nil); err != nil {
        log.Fatalf("Server Shutdown Failed:%+v", err)
    }
    log.Println("Server exited")
}
```

- `/fast`: A quick response, ideal for throughput testing.
- `/slow`: Simulates latency and contention.
- `/gc`: Simulate GC heavy workflow.
- `net/http/pprof`: Exposes runtime profiling on `localhost:6060`.

Run it with:

```
go run main.go
```

## Simulating Load: Tools That Reflect Reality

### When to Use What

Info

This is by no means an exhaustive list. The ecosystem of load-testing tools is broad and constantly evolving. Tools like Apache JMeter, Locust, Artillery, and Gatling each bring their own strengths—ranging from UI-driven test design to distributed execution or JVM-based scenarios. The right choice depends on your stack, test goals, and team workflow. The tools listed here are optimized for Go-based services and local-first benchmarking, but they’re just a starting point.

At a glance, `vegeta`, `wrk`, and `k6` all hammer HTTP endpoints. But they serve different roles depending on what you're testing, how much precision you need, and how complex your scenario is.

| Tool | Focus | Scriptable | Metrics Depth | Ideal Use Case |
| --- | --- | --- | --- | --- |
| `vegeta` | Constant rate load generation | No (but composable) | High (histogram, percentiles) | Tracking latency percentiles over time; CI benchmarking |
| `wrk` | Max throughput stress tests | Yes (Lua) | Medium | Measuring raw server capacity and concurrency limits |
| `k6` | Scenario-based simulation | Yes (JavaScript) | High (VU metrics, dashboards) | Simulating real-world user workflows and pacing |

Use `vegeta` when:

- You need a consistent RPS load (e.g., 100 requests/sec for the 60s).
- You're observing latency degradation under controlled pressure.
- You want structured output (histograms, percentiles) for profiling.
- You want to verify local changes before deeper profiling.

Use `wrk` when:

- You're exploring upper-bound throughput.
- You want raw, fast load with minimal setup.
- You’re profiling at high concurrency (e.g., 10k connections).

Use `k6` when:

- You must model complex flows like login → API call → wait → logout.
- You’re integrating performance tests into CI/CD.
- You want thresholds, pacing, and visual feedback.

Each of these tools has a place in your benchmarking toolkit. Picking the right one depends on whether you're validating performance, exploring scaling thresholds, or simulating end-user behavior.

### Vegeta

[Vegeta](https://github.com/tsenart/vegeta) is a flexible HTTP load testing tool written in Go, built for generating constant request rates. This makes it well-suited for simulating steady, sustained traffic patterns instead of sudden spikes.

We reach for Vegeta when precision matters. It maintains exact request rates and captures detailed latency distributions, which helps track how system behavior changes under load. It’s lightweight, easy to automate, and integrates cleanly into CI workflows—making it a reliable option for benchmarking Go services.

Install:

```
go install github.com/tsenart/vegeta@latest
```

Which endpoint(s) we are going to test:

```
echo "GET http://localhost:8080/slow" > targets.txt
```

Run:

```
vegeta attack -rate=100 -duration=30s -targets=targets.txt | tee results.bin | vegeta report
```

Potential output

```
> vegeta attack -rate=100 -duration=30s -targets=targets.txt | tee results.bin | vegeta report
Requests      [total, rate, throughput]  3000, 100.04, 100.03
Duration      [total, attack, wait]      29.989635542s, 29.989108333s, 527.209µs
Latencies     [mean, 50, 95, 99, max]    524.563µs, 504.802µs, 793.997µs, 1.47362ms, 7.351541ms
Bytes In      [total, mean]              42000, 14.00
Bytes Out     [total, mean]              0, 0.00
Success       [ratio]                    100.00%
Status Codes  [code:count]               200:3000
Error Set:
```

View percentiles:

```
vegeta report -type='hist[0,10ms,50ms,100ms,200ms,500ms,1s]' < results.bin
```

Generate chart:

```
vegeta plot < results.bin > plot.html
```

Testing Multiple Endpoints with Vegeta

Depending on your goals, there are two recommended approaches for testing both `/fast` and `/slow` endpoints in a single run.

**Option 1: Round-Robin Between Endpoints**

Create a `targets.txt` with both endpoints:

```
cat > targets.txt <<EOF
GET http://localhost:8080/fast
GET http://localhost:8080/slow
EOF
```

Run the test:

```
vegeta attack -rate=50 -duration=30s -targets=targets.txt | tee mixed-results.bin | vegeta report -type='hist[0,50ms,100ms,200ms,500ms,1s,2s]'
```

- Requests are randomly distributed between the two endpoints.
- Useful for observing aggregate behavior of mixed traffic.
- Easy to set up and analyze combined performance.

**Option 2: Weighted Mix Using Multiple Vegeta Runs**

To simulate different traffic proportions (e.g., 80% fast, 20% slow):

```
# Send 80% of requests to /fast
vegeta attack -rate=40 -duration=30s -targets=<(echo "GET http://localhost:8080/fast") > fast.bin &

# Send 20% of requests to /slow
vegeta attack -rate=10 -duration=30s -targets=<(echo "GET http://localhost:8080/slow") > slow.bin &

wait
```

Then merge the results and generate a report:

```
vegeta encode fast.bin slow.bin > combined.bin
vegeta report -type='hist[0,50ms,100ms,200ms,500ms,1s,2s]' < combined.bin
```

- Gives you precise control over traffic distribution.
- Better for simulating realistic traffic mixes.
- Enables per-endpoint benchmarking when analyzed separately.

Both methods are valid—choose based on whether you need simplicity or control.

### wrk

[wrk](https://github.com/wg/wrk) is a high-performance HTTP benchmarking tool written in C. It's designed for raw speed and concurrency, making it ideal for stress testing your server’s throughput and connection handling capacity.

We use `wrk` when we want to push the system to its upper limits. It excels at flooding endpoints with high request volumes using multiple threads and connections. While it doesn’t offer detailed percentiles like `vegeta`, it's perfect for quick saturation tests and measuring how much traffic your Go server can handle before it starts dropping requests or stalling.

Install:

```
brew install wrk  # or build from source
```

Run test:

```
wrk -t4 -c100 -d30s http://localhost:8080/fast
```

Potential output

```
> wrk -t4 -c100 -d30s http://localhost:8080/fast
Running 30s test @ http://localhost:8080/fast
  4 threads and 100 connections
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency     1.29ms  255.31us   5.24ms   84.86%
    Req/Sec    19.30k   565.16    21.93k    77.92%
  2304779 requests in 30.00s, 287.94MB read
Requests/sec:  76823.88
Transfer/sec:      9.60MB
```

### k6

[k6](https://k6.io) is a modern load testing tool built around scripting realistic client behavior in JavaScript. It’s designed for simulating time-based load profiles—ramp-up, steady-state, ramp-down—and supports custom flows, pacing, and threshold-based validation.

We use `k6` when raw throughput isn’t enough and we need to simulate how real users interact with the system. It handles chained requests, models session-like flows, and supports stage-based testing out of the box. With rich metrics and seamless CI/CD integration, `k6` helps surface regressions before they reach production.

Install:

```
brew install k6
```

Script:

```
// script.js
import http from 'k6/http';
import { sleep } from 'k6';

export const options = {
  stages: [
    { duration: '10s', target: 50 },
    { duration: '30s', target: 50 },
    { duration: '10s', target: 0 },
  ],
};

export default function () {
  http.get('http://localhost:8080/fast');
  sleep(1);
}
```

Run:

```
k6 run script.js
```

Potential output

```
> k6 run script.js

         /\      Grafana   /‾‾/
    /\  /  \     |\  __   /  /
   /  \/    \    | |/ /  /   ‾‾\
  /          \   |   (  |  (‾)  |
 / __________ \  |_|\_\  \_____/

     execution: local
        script: script.js
        output: -

     scenarios: (100.00%) 1 scenario, 50 max VUs, 1m20s max duration (incl. graceful stop):
              * default: Up to 50 looping VUs for 50s over 3 stages (gracefulRampDown: 30s, gracefulStop: 30s)

  █ TOTAL RESULTS

    HTTP
    http_req_duration.......................................................: avg=495.55µs min=116µs med=449µs max=5.49ms p(90)=705µs p(95)=820.39µs
      { expected_response:true }............................................: avg=495.55µs min=116µs med=449µs max=5.49ms p(90)=705µs p(95)=820.39µs
    http_req_failed.........................................................: 0.00%  0 out of 2027
    http_reqs...............................................................: 2027   40.146806/s

    EXECUTION
    iteration_duration......................................................: avg=1s       min=1s    med=1s    max=1.01s  p(90)=1s    p(95)=1s
    iterations..............................................................: 2027   40.146806/s
    vus.....................................................................: 3      min=3         max=50
    vus_max.................................................................: 50     min=50        max=50

    NETWORK
    data_received...........................................................: 266 kB 5.3 kB/s
    data_sent...............................................................: 176 kB 3.5 kB/s

running (0m50.5s), 00/50 VUs, 2027 complete and 0 interrupted iterations
default ✓ [======================================] 00/50 VUs  50s
```

## Profiling Networked Go Applications with `pprof`

Profiling Go applications that heavily utilize networking is crucial to identifying and resolving bottlenecks that impact performance under high-traffic scenarios. Go's built-in `net/http/pprof` package provides insights specifically beneficial for network-heavy operations. Set up continuous profiling by enabling an HTTP endpoint:

```
import (

    _ "net/http/pprof"

)

// ...

    // Start pprof in a separate goroutine.
    go func() {
        log.Println("pprof listening on :6060")
        if err := http.ListenAndServe("localhost:6060", nil); err != nil {
            log.Fatalf("pprof server error: %v", err)
        }
    }()
```

It's possible to inspect the application in real time, even under heavy load. To capture a CPU profile:

```
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
```

This grabs a 30-second snapshot of CPU usage. With the `pprof` HTTP server exposed, all runtime data—CPU, memory, goroutines, contention, flamegraphs—is available without pausing or restarting the application.

```
go tool pprof -http=:7070 cpu.prof #
```

### CPU Profiling

Profiling a system at rest rarely tells the full story. Real bottlenecks show up under pressure—when requests stack up, threads compete, and memory churn increases. CPU profiling during load reveals where execution time concentrates, often exposing slow serialization, inefficient handler logic, or contention between goroutines. These are the paths that quietly limit throughput and inflate latency when traffic scales.

#### What to Look For

- Network Serialization Hotspots: Frequent use of `json.Marshal` or similar serialization methods during network response generation.
- Syscall Overhead: Extensive syscall usage (e.g., `syscall.Read`) suggesting inefficient socket handling or excessive blocking I/O.
- GC Activity: High frequency of `runtime.gc` indicating inefficient memory management impacting response latency.

**Why This Matters:** Identifying and optimizing CPU-intensive operations in networking contexts reduces latency, boosts throughput, and improves reliability during traffic spikes.

### Flamegraphs

Flamegraphs provide a visual summary of where CPU time is spent by aggregating and collapsing stack traces into a single view. They make it easy to identify performance hotspots without digging through raw profiling data. In server applications, this often highlights issues in request handling, serialization, or blocking I/O. Under load, flamegraphs are especially useful for catching subtle inefficiencies that scale into major performance problems.

#### What to Look For

- Functions related to network I/O or data transfer that appear as wide blocks in a flamegraph often point to excessive time spent on serialization, buffering, or socket operations.
- Deep Call Chains Deep stacks could reveal inefficient middleware or unnecessary layers in network request handling.
- Unexpected Paths Look for unexpected serialization, reflection, or routing inefficiencies.

**Why This Matters:** Flamegraphs simplify diagnosing complex inefficiencies visually, leading to quicker optimization and reduced downtime.

### Managing Garbage Collection (GC) Pressure

Memory profiling helps identify where your application is wasting heap space, especially in network-heavy code paths. Common issues include repeated allocation of response buffers, temporary objects, or excessive use of slices and maps. These patterns often go unnoticed until they trigger GC pressure or latency under load. Profiling with `pprof` follows the same basic steps as CPU profiling, making it easy to integrate into your existing workflow.

```
go tool pprof http://localhost:6060/debug/pprof/heap
```

Then, again, you can view results interactively.

```
go tool pprof -http=:7070 mem.prof #
```

#### What to Look For

- Frequent Temporary Buffers: High frequency of allocations in network buffers, such as repeatedly creating byte slices for each request.
- Persistent Network Objects: Accumulation of long-lived network connections or sessions.
- Excessive Serialization Overhead: High object creation rate due to repeated encoding/decoding of network payloads.

Example: Optimizing buffer reuse using `sync.Pool` greatly reduces GC pressure during high-volume network operations.

**Why This Matters:** Reducing memory churn from network activities improves response times and minimizes latency spikes caused by GC.

### Identifying CPU Bottlenecks

Networked applications often hit CPU limits first when pushed under sustained load. Profiling helps surface where time is actually being spent and what’s getting in the way.

#### What to Look For

- Latency Rising While Throughput Stalls: A sign the CPU is saturated, often from request processing or serialization overhead.
- Scheduler Overhead (runtime.schedule, mcall): Too many goroutines can overwhelm the scheduler, especially when each connection gets its own handler.
- Lock Contention: Repeated locking on shared network state or blocking channel operations slows down throughput and limits parallelism.

For example, if profiling shows excessive time spent in TLS handshake routines, the fix might involve moving handshakes off the hot path or reducing handshake frequency with connection reuse.

**Why it matters:** CPU bottlenecks cap your ability to scale. Fixing them is often the difference between a system that handles 5K clients and one that handles 50K.

### Practicle example of Profiling Networked Go Applications with `pprof`

To illustrate these concepts practically, our demo application integrates profiling and benchmarking tools and provides comprehensive profiling and load testing scenarios. The demo covers identifying performance bottlenecks, analyzing flame graphs, and benchmarking under various simulated network conditions.

Due to its significant size, [Practicle example of Profiling Networked Go Applications with `prof`](https://goperf.dev/02-networking/gc-endpoint-profiling/) is a separate article.

## Benchmarking as a Feedback Loop

A single load test run means little in isolation. But if you treat benchmarking as part of your development cycle—before and after changes—you start building a performance narrative. You can see exactly how a change impacted throughput or whether it traded latency for memory overhead.

The Go standard library gives you `testing.B` for microbenchmarks. Combine profiling with robust integration testing as part of your CI/CD pipeline using tools like `Vegeta` and `k6`. This practice ensures early detection of regressions, continuous validation of performance enhancements, and reliable application performance maintenance under realistic production conditions.
