Source: https://goperf.dev/02-networking/10k-connections/

# Managing 10K+ Concurrent Connections in Go

Why not 100K+ or 1 Mill connection?

While framing the challenge in terms of “100K concurrent connections” is tempting, practical engineering often begins with a more grounded target: 10K to 20K stable, performant connections. This isn’t a limitation of Go itself but a reflection of real-world constraints: ulimit settings, ephemeral port availability, TCP stack configuration, and the nature of the application workload all set hard boundaries.

Cloud environments introduce their own considerations. For instance, AWS Fargate explicitly sets both the soft and hard nofile (number of open files) limit to 65,535, which provides more headroom for socket-intensive applications but still falls short of the 100K+ threshold. On EC2 instances, the practical limits depend on the base operating system and user configuration. By default, many Linux distributions impose a soft limit of 1024 and a hard limit of 65535 for nofile. Even this hard cap is lower than required to handle 100,000 open connections in a single process. Reaching higher limits requires kernel-level tuning, container runtime overrides, and multi-process strategies to distribute file descriptor load.

A server handling simple echo logic behaves very differently from one performing CPU-bound processing, structured logging, or real-time transformation. Additionally, platform-level tunability varies—Linux exposes granular control through sysctl, epoll, and reuseport, while macOS lacks many of these mechanisms. In that context, achieving and sustaining 10K+ concurrent connections with real workloads is a demanding, yet practical, benchmark.

Handling massive concurrency in Go is often romanticized—_"goroutines are cheap, just spawn them!"_—but reality gets harsher as we push towards six-digit concurrency levels. Serving over 10,000 concurrent sockets isn’t something you solve by scaling hardware alone—it requires an architecture that works with the OS, the Go runtime, and the network stack, not against them.

## Embracing Go’s Concurrency Model

Go’s lightweight goroutines and its powerful runtime scheduler make it an excellent choice for scaling network applications. Goroutines consume only a few kilobytes of stack space, which, in theory, makes them ideal for handling tens of thousands of concurrent connections. However, reality forces us to think beyond just spinning up goroutines. While the language’s abstraction makes concurrency almost “magical,” achieving true efficiency at this scale demands intentional design.

Running a server that spawns one goroutine per connection means you’re leaning heavily on the runtime scheduler to juggle thousands of concurrent execution paths. While goroutines are lightweight, they’re not free—each one adds to memory consumption and introduces scheduling overhead that scales with concurrency. Thus, the first design pattern that should be adopted is to ensure that each connection follows a clearly defined lifecycle and that every goroutine performs its task as efficiently as possible.

Let’s consider a basic model where we accept connections and delegate their handling to separate goroutines:

```
package main

import (
    "log"
    "net"
    "sync/atomic"
    "time"
)

var activeConnections uint64

func main() {
    listener, err := net.Listen("tcp", ":8080")
    if err != nil {
        log.Fatalf("Error starting TCP listener: %v", err)
    }
    defer listener.Close()

    for {
        conn, err := listener.Accept()
        if err != nil {
            log.Printf("Error accepting connection: %v", err)
            continue
        }

        atomic.AddUint64(&activeConnections, 1)
        go handleConnection(conn)
    }
}

func handleConnection(conn net.Conn) {
    defer func() {
        conn.Close()
        atomic.AddUint64(&activeConnections, ^uint64(0)) // effectively decrements the counter
    }()

    // Imagine complex processing here—an echo server example:
    buffer := make([]byte, 1024)
    for {
        conn.SetDeadline(time.Now().Add(30 * time.Second)) // prevent idle hangs
        n, err := conn.Read(buffer)
        if err != nil {
            log.Printf("Connection read error: %v", err)
            return
        }
        _, err = conn.Write(buffer[:n])
        if err != nil {
            log.Printf("Connection write error: %v", err)
            return
        }
    }
}
```

Each connection is assigned its own goroutine. That approach works fine at low concurrency and fits Go’s model well. But once you’re dealing with tens of thousands of connections, the design has to account for system limits. Goroutines are cheap—but not free.

### Managing Concurrency at Scale

It’s not enough to just accept connections; you need to control what happens after. Unbounded goroutine creation leads to memory growth and increased scheduler load. To keep the system stable, concurrency must be capped—typically using a semaphore or similar construct to limit how many goroutines handle active work at any given time.

For example, you might limit the number of simultaneous active connections before spinning up a new goroutine for each incoming connection. This strategy might involve a buffered channel acting as a semaphore:

```
package main

import (
    "net"
)

var connLimiter = make(chan struct{}, 10000) // Max 10K concurrent conns

func main() {
    ln, _ := net.Listen("tcp", ":8080")
    defer ln.Close()

    for {
        conn, _ := ln.Accept()

        connLimiter <- struct{}{} // Acquire slot
        go func(c net.Conn) {
            defer func() {
                c.Close()
                <-connLimiter // Release slot
            }()
            // Dummy echo logic
            buf := make([]byte, 1024)
            c.Read(buf)
            c.Write(buf)
        }(conn)
    }
}
```

This pattern not only helps prevent resource exhaustion but also gracefully degrades service under high load. Adjusting these limits according to your hardware and workload characteristics is a continuous tuning process.

Info

We use the `connLimiter` approach here for purely illustrative purposes, as it clarifies the idea. In real life, you will most likely use [errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup) to manage the goroutines amount and some `SIGINT,` and `SIGTERM` signal handling for graceful process termination.

### OS-Level and Socket Tuning

Before your Go application can handle more than 10,000 simultaneous connections, the operating system has to be prepared for that scale. On Linux, this usually starts with raising the limit on open file descriptors. The TCP stack also needs tuning—default settings often aren’t designed for high-connection workloads. Without these adjustments, the application will hit OS-level ceilings long before Go becomes the bottleneck.

```
# Increase file descriptor limit
ulimit -n 200000
```

But it doesn’t stop there. You’ll also need:

```
sysctl -w net.core.somaxconn=65535
sysctl -w net.ipv4.ip_local_port_range="10000 65535"
sysctl -w net.ipv4.tcp_tw_reuse=1
sysctl -w net.ipv4.tcp_fin_timeout=15
```

- `net.core.somaxconn=65535`: This controls the size of the pending connection queue (the backlog) for listening sockets. A small value here will cause connection drops when many clients attempt to connect simultaneously.
- `net.ipv4.ip_local_port_range="10000 65535"`: Defines the ephemeral port range used for outbound connections. A wider range prevents port exhaustion when you’re making many outbound connections from the same machine.
- `net.ipv4.tcp_tw_reuse=1`: Allows reuse of sockets in `TIME_WAIT` state for new connections if safe. Helps reduce socket exhaustion, especially in short-lived TCP connections.
- `net.ipv4.tcp_fin_timeout=15`: Reduces the time the kernel holds sockets in `FIN_WAIT2` after a connection is closed. Shorter timeout means faster resource reclamation, crucial when thousands of sockets churn per minute.

Tuning these parameters helps prevent the OS from becoming the bottleneck as connection counts grow. On top of that, setting socket options like `TCP_NODELAY` can reduce latency by disabling [Nagle’s algorithm](https://en.wikipedia.org/wiki/Nagle%27s_algorithm), which buffers small packets by default. In Go, these options can be applied through the net package, or more directly via the syscall package if lower-level control is needed.

In some cases, using Go’s `net.ListenConfig` allows you to inject custom control over socket creation. This is particularly useful when you need to set options at the time of listener creation:

```
func main() {
    lc := net.ListenConfig{
        Control: func(network, address string, c syscall.RawConn) error {
            var controlErr error
            err := c.Control(func(fd uintptr) {
                // Enable TCP_NODELAY on the socket
                controlErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1)
            })
            if err != nil {
                return err
            }
            return controlErr
        },
    }
    listener, err := lc.Listen(context.Background(), "tcp", ":8080")
    if err != nil {
        log.Fatalf("Error creating listener: %v", err)
    }
    defer listener.Close()
    // Accept connections in a loop…
}
```

### Go Scheduler and Memory Pressure

Spawning 10,000 goroutines might look impressive on paper, but what matters is how those goroutines behave. If they’re mostly idle—blocked on I/O like network or disk—Go’s scheduler handles them efficiently, parking and resuming with little overhead. But when goroutines actively allocate memory, spin in tight loops, or constantly contend on channels and mutexes, things get expensive. You’ll start to see increased garbage collection pressure and scheduler thrashing, both of which erode performance.

Go’s garbage collector handles short-lived allocations well, but it doesn’t come for free. If you’re spawning goroutines that churn through memory—allocating per request, per message, or worse, per loop—GC pressure builds fast. The result isn’t just more frequent collections, but higher latency and lost CPU cycles. Throughput drops, and the system spends more time cleaning up than doing real work.

To manage this, you can explicitly tune the GC aggressiveness:

```
GOGC=50
```

Or directly within your codebase:

```
import "runtime/debug"

func main() {
    debug.SetGCPercent(50)
    // rest of your application logic
}
```

The default value for `GOGC` is 100, meaning the GC triggers when the heap size doubles compared to the previous GC cycle. Lower values (like 50) mean more frequent but shorter GC cycles, helping control memory growth at the cost of increased CPU overhead.

Info

In some cases, you may need an opposite – [to increase the `GOGC` value, turn the GC off completely](https://goperf.dev/01-common-patterns/gc/#gc-tuning-gogc), or prefer [GOMEMLIMIT=X and GOGC=off](https://goperf.dev/01-common-patterns/gc/#gomemlimitx-and-gogcoff-configuration) configuration. **Do not make a decision before careful profiling!**

### Optimizing Goroutine Behavior

Consider structuring your application so that goroutines block naturally rather than actively waiting or spinning. For example, instead of polling channels in tight loops, use select statements efficiently:

```
for {
    select {
    case msg := <-msgChan:
        handleMsg(msg)
    case <-ctx.Done():
        return
    }
}
```

If your goroutines must wait, prefer blocking on channels or synchronization primitives provided by Go, like mutexes or condition variables, instead of actively polling.

### Pooling and Reusing Objects

Another crucial technique to reduce memory allocations and GC overhead [is using `sync.Pool`](https://goperf.dev/01-common-patterns/object-pooling/):

```
var bufPool = sync.Pool{
    New: func() any { return make([]byte, 1024) },
}

func handleRequest() {
    buf := bufPool.Get().([]byte)
    defer bufPool.Put(buf)  // 

    // use buffer for request handling
}
```

Reusing objects through pools reduces memory churn. With fewer allocations, the garbage collector runs less often and with less impact. This translates directly into lower latency and more predictable performance under load.

### Connection Lifecycle Management

A connection isn’t just accepted and forgotten—it moves through a full lifecycle: setup, data exchange, teardown. Problems usually show up in the quiet phases. Idle connections that aren’t cleaned up can tie up memory and block goroutines indefinitely. Enforcing read and write deadlines is essential. Heartbeat messages help too—they give you a way to detect dead peers without waiting for the OS to time out.

In one production case, slow client responses left goroutines blocked in reads. Over time, they built up until the system started degrading. Adding deadlines and lightweight health checks fixed the leak. Goroutines no longer lingered, and resource usage stayed flat under load.

Each connection still runs in its own goroutine—but with proper lifecycle management in place, scale doesn’t come at the cost of stability.

```
for {
    conn, err := ln.Accept()
    if err != nil {
        // handle error
    }
    go handle(conn)
}
```

Inside the handler, a ticker is used to fire every few seconds, triggering a periodic heartbeat that keeps the connection active and responsive:

```
ticker := time.NewTicker(5 * time.Second)
defer ticker.Stop()
```

Before reading from the client, the server sets a read deadline—if no data is received within that time, the operation fails, and the connection is cleaned up. This prevents a blocked read from stalling the goroutine indefinitely:

```
conn.SetReadDeadline(time.Now().Add(10 * time.Second))
_, err := reader.ReadString('
')
if err != nil {
    return // read timeout or client gone
}
```

Likewise, before sending the heartbeat, the server sets a write deadline. If the client is unresponsive or the network is slow, the write will fail promptly, avoiding resource leakage:

```
select {
case <-ticker.C:
    conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
    conn.Write([]byte("ping"))
default:
    // skip heartbeat if not due
}
```

The loop handles incoming messages and sends periodic heartbeats, with read and write deadlines enforcing boundaries on both sides. This setup keeps each connection under active supervision. Silent failures don’t linger, and the system avoids trading stability for performance.

## Real-World Tuning and Scaling Pitfalls

Scaling to 10K+ connections is not just a matter of code—it requires anticipating and mitigating potential pitfalls across many layers of the stack. Beyond addressing memory footprint, file descriptor limits, and blocking I/O, a series of high-concurrency echo server tests revealed additional performance considerations under real load.

One experiment began with a simple line-based echo server. The baseline handler was straightforward:

```
func handle(conn net.Conn) {
    defer conn.Close()
    reader := bufio.NewReader(conn)

    for {
        line, err := reader.ReadString('\n')
        if err != nil {
            fmt.Printf("Connection closed: %v\n", err)
            return
        }
        conn.Write([]byte(line)) // echo
    }
}
```

Using a tool like `tcpkali`:

```
tcpkali -m $'ping\n' -c 10000 --connect-rate=2000 --duration=60s 127.0.0.1:9000
```

The test ramped up to 10'000 concurrent connections. Over the 60-second run, it sent 2.4 MiB and received 210.3 MiB of data. Each connection averaged around 0.4 kBps, with an aggregate throughput of 29.40 Mbps downstream and 0.33 Mbps upstream. This result highlighted the server’s limited responsiveness to outgoing data under sustained high concurrency, with substantial backpressure on `fd.Read`.

### Instrumenting and Benchmarking the Server

Info

We use `c5.2xlarge` (8 CPU, 16 GiB) AWS instance for all these tests.

To better understand system behavior under high load, Go’s built-in tracing facilities were enabled:

```
import (
    "runtime/trace"
    "os"
    "log"
)

func main() {
    f, err := os.Create("trace.out")
    if err != nil { log.Fatal(err) }
    defer f.Close()

    trace.Start(f)
    defer trace.Stop()

    // server logic ...
}
```

After running the server and collecting traces, the command

```
go tool trace trace.out
```

revealed that a significant portion of runtime was spent blocked in `fd.Read` and `fd.Write`, suggesting an opportunity to balance I/O operations more effectively. Trace analysis revealed that `fd.Read` accounted for 23% of runtime, while `fd.Write` consumed 75%, indicating significant write-side backpressure during echoing. Although `ulimit -n` was set to 65535 (AWS EC2 instance's hard limit), the system still encountered bottlenecks due to I/O blocking and ephemeral port range limitations.

### Reducing Write Blocking with Buffered Writes

Connection writes were wrapped in a `bufio.Writer` with periodic flushing instead of flushing after each write. The updated snippet:

```
reader := bufio.NewReader(conn)
writer := bufio.NewWriter(conn)
count := 0
const flushInterval = 10

for {
    line, err := reader.ReadString('\n')
    if err != nil {
        return
    }
    writer.WriteString(line)
    count++
    if count >= flushInterval {
        writer.Flush()
        count = 0
    }
}
```

Benchmarking with:

```
tcpkali -m $'ping\n' -c 10000 --connect-rate=2000 --duration=60s 127.0.0.1:9000
```

showed dramatic improvements—throughput increased from about 33.8 MiB to over 1661 MiB received and 1369 MiB sent across 10,000 connections, with per-connection bandwidth reaching 5.3 kBps. Aggregate throughput rose to 232.28 Mbps downstream and 191.41 Mbps upstream. The tracing profile confirmed more balanced I/O wait times, even under a much heavier concurrent load.

### Handling Burst Loads and CPU-Bound Workloads

To evaluate the server's behavior under extreme connection pressure, a burst test was executed with 30,000 connections ramping up at 5,000 per second:

```
tcpkali -m $'ping\n' -c 30000 --connect-rate=5000 --duration=60s 127.0.0.1:9000
```

The server ramped up cleanly to 30,000 concurrent connections and sustained them for the full 60 seconds. It handled a total of 2580.3 MiB sent and 1250.9 MiB received, maintaining an aggregate throughput of 360.75 Mbps upstream and 174.89 Mbps downstream. Per-channel bandwidth naturally decreased to about 1.2 kBps, but the stability across all channels and the lack of dropped connections pointed to effective load distribution and solid I/O handling even at scale.

To simulate CPU-bound workloads, the server was modified to compute a SHA256 hash for each incoming line:

```
func hash(s string) string {
    h := sha256.Sum256([]byte(s))
    return hex.EncodeToString(h[:])
}

...

for {
    line, err := reader.ReadString('\n')
    if err != nil {
        return
    }
    _ = hash(line) // simulate CPU-intensive processing
    writer.WriteString(line)
    count++
    if count >= flushInterval {
        writer.Flush()
        count = 0
    }
}
```

In this configuration, using the same 30,000-connection setup, throughput dropped to 1068.3 MiB sent and 799.3 MiB received. Aggregate bandwidth fell to 149.35 Mbps upstream and 111.74 Mbps downstream, and per-connection bandwidth declined to around 0.7 kBps. While the server maintained full connection count and uptime, trace analysis revealed increased time spent in runtime.systemstack\_switch and GC-related functions. This clearly demonstrated the impact of compute-heavy tasks on overall throughput and reinforced the need for careful balance between I/O and CPU workload when operating at high concurrency.

### Summarizing the Technical Gains

Benchmarking across four distinct server configurations revealed how buffering, concurrency scaling, and CPU-bound tasks influence performance under load:

| Feature | Baseline (10K, no buffer) | 10K Buffered Connections | 30K Buffered Connections | 30K + CPU Load (SHA256) |
| --- | --- | --- | --- | --- |
| Connections handled | 10,000 | 10,000 | 30,000 | 30,000 |
| Data sent (60s) | 2.4 MiB | 1369.1 MiB | 2580.3 MiB | 1068.3 MiB |
| Data received (60s) | 210.3 MiB | 1661.4 MiB | 1250.9 MiB | 799.3 MiB |
| Per-channel bandwidth | ~0.4 kBps | ~5.3 kBps | ~1.2 kBps | ~0.7 kBps |
| Aggregate bandwidth (↓/↑) | 29.40 / 0.33 Mbps | 232.28 / 191.41 Mbps | 174.89 / 360.75 Mbps | 111.74 / 149.35 Mbps |
| Packet rate estimate (↓/↑) | 329K / 29 pkt/s | 278K / 16K pkt/s | 135K / 32K pkt/s | 136K / 13K pkt/s |
| I/O characteristics | Severe write backpressure | Balanced read/write | Efficient under scale | Latency from CPU contention |
| CPU and GC pressure | Low | Low | Moderate | High (GC + hash compute) |

Starting from the baseline of 10,000 unbuffered connections, the server showed limited throughput—just 2.4 MiB sent and 210.3 MiB received over 60 seconds—with clear signs of write-side backpressure. Introducing buffered writes with the same connection count unlocked over 1369 MiB sent and 1661 MiB received, improving throughput by more than an order of magnitude and balancing I/O wait times. Scaling further to 30,000 connections maintained stability and increased overall throughput, albeit with reduced per-connection bandwidth. When SHA256 hashing was added per message, total throughput dropped significantly, confirming the expected CPU bottleneck and reinforcing the need to factor in compute latency when designing high-concurrency, I/O-heavy services.

These profiles serve as a concrete reference for performance-aware development, where transport, memory, and compute must be co-optimized for real-world scalability.

As you can see, achieving even 30,000 concurrent connections with reliable performance is a non-trivial task. The test results demonstrated that once a workload deviates from a trivial echo server—for example, by adding logging, CPU-bound processing, or more complex read/write logic—throughput and stability can degrade rapidly. Performance at scale is highly dependent on workflow characteristics, such as I/O patterns, synchronization frequency, and memory pressure.

Taken together, these tests reinforce the need for workload-aware tuning and platform-specific adjustments when building high-performance, scalable networking systems.
