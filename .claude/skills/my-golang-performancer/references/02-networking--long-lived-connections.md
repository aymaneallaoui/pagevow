Source: https://goperf.dev/02-networking/long-lived-connections/

# Memory Management and Leak Prevention in Long-Lived Connections

Long-lived connections—such as WebSockets or TCP streams—are critical for real-time systems but also prone to gradual degradation. When connections persist, any failure to clean up buffers, goroutines, or timeouts can quietly consume memory over time. These leaks often evade unit tests or staging environments but surface under sustained load in production.

This article focuses on memory management strategies tailored to long-lived connections in Go. It outlines patterns that cause leaks, techniques for enforcing resource bounds, and tools to identify hidden retention through profiling.

## Identifying Common Leak Patterns

In garbage-collected languages like Go, memory leaks typically involve lingering references—objects that are no longer needed but remain reachable. The most common culprits in connection-heavy services include goroutines that don’t exit, buffered channels that accumulate data, and slices that retain large backing arrays.

### Goroutine Leaks

Handlers for persistent connections often run in their own goroutines. If the control flow within a handler blocks indefinitely—whether due to I/O operations, nested goroutines, or external dependencies—those goroutines can remain active even after the connection is no longer useful.

```
func handleWS(conn *websocket.Conn) {
    for {
        _, message, err := conn.ReadMessage()
        if err != nil {
            break
        }
        process(message)
    }
}

http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
    ws, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        return
    }
    go handleWS(ws)
})
```

Here, if `process(message)` internally spawns goroutines without proper cancellation, or if `conn.ReadMessage()` blocks indefinitely after a network interruption, the handler goroutine can hang forever, retaining references to stacks and heap objects. Blocking reads prevent the loop from exiting, and unbounded goroutine spawning within `process` can accumulate if upstream errors aren’t handled. Now multiply by 10,000 connections.

### Buffer and Channel Accumulation

Buffered channels and pooled buffers offer performance advantages, but misuse can lead to retained memory that outlives its usefulness. A typical example involves `sync.Pool` combined with I/O:

```
var bufferPool = sync.Pool{
    New: func() interface{} { return make([]byte, 4096) },
}

func handle(conn net.Conn) {
    buf := bufferPool.Get().([]byte)
    defer bufferPool.Put(buf)

    for {
        n, err := conn.Read(buf)
        if err != nil {
            return
        }

        data := make([]byte, n)
        copy(data, buf[:n])
        go process(data)
    }
}
```

This version correctly isolates the active portion of the buffer using a copy. Problems arise when the copy is skipped:

```
data := buf[:n]
go process(data)
```

Although `data` appears small, it still points to the original 4 KB buffer. If `process` stores that slice in a log queue, cache, or channel, the entire backing array remains in memory. Over time, this pattern can hold onto hundreds of megabytes of heap space across thousands of connections.

To prevent this, always create a new slice with just the required data length before handing it off to any code that might retain it. Copying a slice may seem inefficient, but it ensures the larger buffer is no longer indirectly referenced.

## Enforcing Read/Write Deadlines

Network I/O without deadlines introduces an unbounded wait time. If a client stalls or a network failure interrupts a connection, read and write operations may block indefinitely. In a high-connection environment, even a few such blocked goroutines can accumulate and exhaust memory over time.

Deadlines solve this by imposing a strict upper bound on how long any read or write can take. Once the deadline passes, the operation returns with a timeout error, allowing the connection handler to proceed with cleanup.

### Setting Deadlines

```
const timeout = 30 * time.Second

func handle(conn net.Conn) {
    defer conn.Close()

    buffer := make([]byte, 4096) // 4 KB buffer; size depends on protocol and usage

    for {
        conn.SetReadDeadline(time.Now().Add(timeout))
        n, err := conn.Read(buffer)
        if err != nil {
            if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
                return
            }
            return
        }

        conn.SetWriteDeadline(time.Now().Add(timeout))
        _, err = conn.Write(buffer[:n])
        if err != nil {
            return
        }
    }
}
```

This approach ensures that each read and write completes—or fails—within a known time window. It prevents handlers from hanging due to slow or unresponsive peers and contributes directly to keeping goroutine count and memory usage stable under load.

### Context-Based Cancellation

For more coordinated shutdowns, contexts provide a way to propagate cancellation signals across multiple goroutines and resources:

```
func handle(ctx context.Context, conn net.Conn) {
    defer conn.Close()

    ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()

    done := make(chan struct{})
    go func() {
        select {
        case <-ctx.Done():
            conn.Close()
        case <-done:
        }
    }()

    // perform read/write as before
    close(done)
}
```

With this pattern, the handler exits cleanly even if the read or write blocks. Closing the connection from a context cancellation path ensures dependent routines terminate in a timely manner.

## Managing Backpressure

When input arrives faster than it can be processed or sent downstream, backpressure is necessary to avoid unbounded memory growth. Systems that ingest data without applying pressure controls can suffer from memory spikes, GC churn, or latency cliffs under load.

### Rate Limiting and Queuing

Rate limiters constrain processing throughput to match downstream capacity. Token-bucket implementations are common and provide burst-friendly rate control

```
type RateLimiter struct {
    tokens chan struct{}
}

func NewRateLimiter(rate int) *RateLimiter {
    rl := &RateLimiter{tokens: make(chan struct{}, rate)}
    for i := 0; i < rate; i++ {
        rl.tokens <- struct{}{}
    }
    go func() {
        ticker := time.NewTicker(time.Second)
        for range ticker.C {
            select {
            case rl.tokens <- struct{}{}:
            default:
            }
        }
    }()
    return rl
}

func (rl *RateLimiter) Acquire() {
    <-rl.tokens
}
```

This limiter can be used per connection or across the system:

```
rl := NewRateLimiter(100)
for {
    rl.Acquire()
    // read/process/send
}
```

By limiting processing rates, the system avoids overwhelming internal queues or consumers. When capacity is reached, the limiter naturally applies backpressure by blocking.

### Flow Control via TCP

For TCP streams, it’s often better to leverage kernel-level flow control rather than building large user-space buffers. This is especially important when sending data to slow or unpredictable clients.

```
type framedConn struct {
    net.Conn
    mw *bufio.Writer
}

func (f *framedConn) WriteFrame(data []byte) error {
    if err := binary.Write(f.mw, binary.BigEndian, uint32(len(data))); err != nil {
        return err
    }
    if _, err := f.mw.Write(data); err != nil {
        return err
    }
    return f.mw.Flush()
}
```

By flushing early and using small buffers, the application shifts pressure back to the TCP stack. If the peer can’t keep up, send calls will block instead of buffering excessive data in memory.

Warning

While relying on TCP’s built-in flow control simplifies the memory model and offloads queuing to the kernel, this approach comes with tradeoffs. Flushing small buffers aggressively forces the application to send many small TCP segments. This increases system call frequency, consumes more CPU per byte, and fragments data on the wire. In high-throughput systems or over high-latency links, this can underutilize the TCP congestion window and cap throughput well below the link’s capacity.

Another risk lies in how TCP flow control applies uniformly to the socket, without application-level context. A slow-reading client can cause Write calls to block indefinitely, holding up goroutines and potentially stalling outbound flows. In fan-out scenarios—like broadcasting to many WebSocket clients—this means one slow recipient can back up others unless additional timeout or buffering logic is applied. While TCP ensures fairness and correctness, it doesn’t help with per-client pacing, prioritization, or partial delivery strategies that might be required in real-world systems.

For low-concurrency or control-plane connections, TCP backpressure alone might be sufficient. But at scale, especially when dealing with mixed client speeds, it’s often necessary to combine TCP-level backpressure with bounded user-space queues, write timeouts, or selective message drops to keep latency predictable and memory usage under control.

---

Persistent connections introduce long-lived resources that must be managed explicitly. Without deadlines, bounded queues, and cleanup coordination, even well-intentioned code can gradually degrade. Apply these patterns consistently and validate them under load with memory profiles.

Memory leaks are easier to prevent than to detect. Defensive design around goroutines, buffers, and timeouts ensures services remain stable even under sustained connection load.
