Source: https://goperf.dev/02-networking/connection_observability/

# Connection Lifecycle Observability: From Dial to Close

Many observability systems expose only high-level HTTP metrics, but deeper insight comes from explicitly instrumenting each stage — DNS resolution, dialing, handshake, negotiation, reads/writes, and teardown. Observing the full lifecycle of a network connection provides the clarity needed to diagnose latency issues, identify failures, and relate resource usage to external behavior.

## DNS Resolution

Every outbound connection begins with a name resolution step unless an IP is already known. DNS latency and failures often dominate connection setup time and can vary significantly depending on caching and resolver performance.

Capturing DNS resolution duration, errors and resulting address provides visibility into one of the least predictable phases. At this stage, it is valuable to:

- measure the time from query issuance to receiving an answer
- log the resolved IP addresses
- surface transient and permanent failures distinctly

In Go, this can be achieved by wrapping the `net.Resolver`.

```
import (
    "context"
    "log"
    "net"
    "time"
)

func resolveWithTracing(ctx context.Context, hostname string) ([]string, error) {
    start := time.Now()
    ips, err := net.DefaultResolver.LookupHost(ctx, hostname)
    elapsed := time.Since(start)

    log.Printf("dns: host=%s duration=%s ips=%v err=%v", hostname, elapsed, ips, err)
    return ips, err
}
```

This explicit measurement avoids relying on opaque metrics exposed by the OS resolver or libraries.

## Dialing

After obtaining an address, the next phase is dialing — establishing a TCP (or other transport) connection. Here, round-trip latency to the target, route stability, and ephemeral port exhaustion can all surface.

Observing the dial phase often involves intercepting `net.Dialer`.

```
func dialWithTracing(ctx context.Context, network, addr string) (net.Conn, error) {
    var d net.Dialer
    start := time.Now()
    conn, err := d.DialContext(ctx, network, addr)
    elapsed := time.Since(start)

    log.Printf("dial: addr=%s duration=%s err=%v", addr, elapsed, err)
    return conn, err
}
```

Why trace here? Dialing failures can indicate downstream unavailability, but also local issues like SYN flood protection

or bad routing. Without a per-dial timestamp and error trace, identifying the locus of failure is guesswork.

## Handshake and Negotiation

For secure connections, the next stage — cryptographic handshake — dominates. TLS negotiation can involve multiple round-trips, certificate validation, and cipher negotiation. Measuring this stage separately is necessary because it isolates pure network latency (dial) from cryptographic and policy enforcement costs (handshake).

The `crypto/tls` library in Go allows instrumentation of the handshake explicitly.

```
import (
    "crypto/tls"
)

func handshakeWithTracing(conn net.Conn, config *tls.Config) (*tls.Conn, error) {
    tlsConn := tls.Client(conn, config)
    start := time.Now()
    err := tlsConn.Handshake()
    elapsed := time.Since(start)

    log.Printf("handshake: duration=%s err=%v", elapsed, err)
    return tlsConn, err
}
```

A common misconception is that slow TLS handshakes always reflect bad network conditions; in practice, slow certificate validation or [OCSP](https://en.wikipedia.org/wiki/Online_Certificate_Status_Protocol)/[CRL](https://en.wikipedia.org/wiki/Certificate_revocation_list) checks are frequently to blame. Separating these phases helps pinpoint the cause.

## Application Reads and Writes

Once the connection is established and negotiated, application-level traffic proceeds as reads and writes. Observability at this stage is often the least precise yet most critical for correlating client-perceived latency to backend processing.

Instrumenting reads and writes directly yields fine-grained latency and throughput metrics. Wrapping a connection is a common strategy.

```
type tracedConn struct {
    net.Conn
}

func (c *tracedConn) Read(b []byte) (int, error) {
    start := time.Now()
    n, err := c.Conn.Read(b)
    elapsed := time.Since(start)

    log.Printf("read: bytes=%d duration=%s err=%v", n, elapsed, err)
    return n, err
}

func (c *tracedConn) Write(b []byte) (int, error) {
    start := time.Now()
    n, err := c.Conn.Write(b)
    elapsed := time.Since(start)

    log.Printf("write: bytes=%d duration=%s err=%v", n, elapsed, err)
    return n, err
}
```

Why measure at this granularity? Reads and writes can block for reasons unrelated to connection establishment — buffer backpressure, TCP window exhaustion, or application pauses. Tracing them reveals whether observed slowness is due to I/O contention or upstream latency.

Warning

Such a granularity level can affect your system performance. See [Why reduce logs?](https://goperf.dev/02-networking/connection_observability/#why-reduce-logs) for more details.

## Teardown

Finally, connection closure often receives little attention, yet it can affect resource usage and connection pool behavior. Observing how long a connection takes to close and whether any errors surface during `Close()` is also useful. For TCP, the closure can be delayed by the OS until all `FIN`/`ACK` sequences complete, and sockets can linger in `TIME_WAIT`

. Explicitly logging teardown metrics helps identify resource leaks.

```
func closeWithTracing(c net.Conn) error {
    start := time.Now()
    err := c.Close()
    elapsed := time.Since(start)

    log.Printf("close: duration=%s err=%v", elapsed, err)
    return err
}
```

## Correlating Spans and Errors

Capturing individual phase durations is not enough unless correlated into a coherent trace. Using a context-aware tracing library allows attaching phase spans to a single logical transaction.

For example, with [OpenTelemetry](https://opentelemetry.io/):

```
import (
    "go.opentelemetry.io/otel/trace"
)

func tracePhase(ctx context.Context, tracer trace.Tracer, phase string, fn func() error) error {
    ctx, span := tracer.Start(ctx, phase)
    defer span.End()
    return fn()
}
```

Wrapping each phase in a span enables stitching the timeline together and correlating specific errors with their originating stage. This level of detail is important when diagnosing production outages where symptoms often manifest far from their root cause.

## Detecting and Explaining Hangs

One of the more insidious problems in distributed systems is a hung connection — no forward progress but no immediate failure. Granular observability can distinguish between a hang during DNS, an unresponsive handshake, a blocked write due to full buffers, or a delayed FIN. For example, if the dial phase logs complete but handshake never finishes, attention should focus on certificate validation, MTU black holes, or middlebox interference. By maintaining per-phase timeouts and alerts based on historical baselines, such conditions can be detected and explained rather than simply categorized as "slow." This explains not just what failed, but why it failed.

## Beyond Logs: Metrics and Structured Events

Most likely you will need to reduce the volume of logs without losing essential information is critical for operating at scale. High-frequency, detailed logging of connection lifecycle events incurs I/O, CPU, and storage overhead. Without control mechanisms, it can overwhelm log aggregation systems, inflate costs, and even obscure the signal with noise. Log verbosity must therefore be managed carefully — keeping enough detail for diagnosis but avoiding excessive volume.

[Zap](https://github.com/uber-go/zap) is used in these examples to demonstrate strategies for runtime configurability and structured logging, but the same principles apply to any capable logging library. The ability to change levels dynamically, emit structured data, and sample events makes it easier to implement these strategies.

### Why reduce logs?

Logging at fine granularity across thousands of connections per second can quickly generate terabytes of data, with much of it being repetitive and low-value. High log throughput can also cause contention with the application’s main execution paths, affecting latency. Furthermore, unbounded log growth burdens operators, complicates incident triage, and increases retention costs.

### Techniques to control log volume

- **Configurable log levels**: adjust verbosity at runtime depending on the situation — keeping production at `INFO` or `WARN` by default, switching to `DEBUG` or `TRACE` during investigation.
- **Sampling**: log only a subset of events under high load or log every Nth connection. Zap supports samplers that enforce rate limits and randomness.
- **Metrics-first, logs-for-anomalies**: promote phase durations and counters to metrics (e.g., Prometheus) and emit logs only when thresholds or percentiles are breached.
- **Aggregate phase data**: collect all phase timings and results per connection, then emit a single structured log event at the end instead of logging each phase as a separate line.

By combining these approaches, it is possible to retain meaningful observability with manageable overhead. Zap’s API simply illustrates how to implement these patterns effectively — **the key takeaway is that the design itself, not the library choice**, determines observability quality and cost.

Here are a few illustrative code snippets for these techniques:

#### Configurable log level

```
atomicLevel := zap.NewAtomicLevel()
logger := zap.New(zapcore.NewCore(
    zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
    zapcore.AddSync(os.Stdout),
    atomicLevel,
))

// Adjust at runtime
atomicLevel.SetLevel(zap.WarnLevel)
```

#### Sampling logs

```
core := zapcore.NewSamplerWithOptions(
    logger.Core(),
    time.Second,
    100, // first 100 per second
    10,  // thereafter
)
logger := zap.New(core)
```

#### Aggregate per-connection log

```
phases := map[string]time.Duration{
    "dns": 10 * time.Millisecond,
    "dial": 40 * time.Millisecond,
    "handshake": 50 * time.Millisecond,
}
logger.Info("connection completed", zap.Any("phases", phases))
```

While log statements and spans provide valuable information, a production-grade observability system benefits greatly from structured metrics collection and structured logging.

Prometheus is well-suited for collecting and querying time-series metrics from each phase, exposing them to dashboards and alerting rules. Instead of embedding phase durations in unstructured logs, it is advisable to design and expose clearly named metrics like `connection_dns_duration_seconds`, `connection_dial_errors_total`, and similar counters. These names are proposed examples that follow Prometheus naming conventions and should be defined explicitly in the implementation to ensure clarity and consistency.

For structured logging, libraries such as Uber’s `zap` offer fast, JSON-encoded logs that integrate cleanly with log aggregation systems like ELK or Splunk. Rather than free-form `Printf`, one can emit structured key-value pairs:

```
import (
    "go.uber.org/zap"
)

var logger, _ = zap.NewProduction()

logger.Info("dns",
    zap.String("host", hostname),
    zap.Duration("duration", elapsed),
    zap.Strings("ips", ips),
    zap.Error(err),
)
```

Structured logs reduce the burden on downstream parsers and make correlating events between services more reliable. Combining these techniques — phase-level instrumentation, Prometheus metrics, and structured logs — transforms opaque failures into actionable insight. With this level of granularity and consistency, ambiguous symptoms become precise diagnoses, ensuring that both developers and operators can pinpoint not just that something is wrong, but exactly where and why.
