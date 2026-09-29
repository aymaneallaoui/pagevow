Source: https://goperf.dev/01-common-patterns/context/

# Efficient Context Management

Whether you're handling HTTP requests, coordinating worker goroutines, or querying external services, there's often a need to cancel in-flight operations or enforce execution deadlines. Go’s `context` package is designed for precisely that—it provides a consistent and thread-safe way to manage operation lifecycles, propagate metadata, and ensure resources are cleaned up promptly.

## Why Context Matters

Go provides two base context constructors: `context.Background()` and `context.TODO()`.

- `context.Background()` is the root context typically used at the top level of your application—such as in `main`, `init`, or server setup—where no existing context is available.
- `context.TODO()` is a placeholder used when it’s unclear which context to use, or when the surrounding code hasn’t yet been fully wired for context propagation. It serves as a reminder that the context logic needs to be filled in later.

The `context` package in Go is designed to carry deadlines, cancellation signals, and other request-scoped values across API boundaries. It's especially useful in concurrent programs where operations need to be coordinated and canceled cleanly.

A typical context workflow begins at the entry point of a program or request—like an HTTP handler, main function, or RPC server. From there, a base context is created using `context.Background()` or `context.TODO()`. This context can then be extended using constructors like:

- `context.WithCancel(parent)` to create a cancelable context.
- `context.WithTimeout(parent, duration)` to cancel automatically after a specific time.
- `context.WithDeadline(parent, time)` for cancelling at a fixed moment.
- `context.WithValue(parent, key, value)` to attach request-scoped data.

Each of these functions returns a new context that wraps its parent. Cancellation signals, deadlines, and values are automatically propagated down the call stack. When a context is canceled—either manually or by timeout—any goroutines or functions listening on `<-ctx.Done()` are immediately notified.

By passing context explicitly through function parameters, you avoid hidden dependencies and gain fine-grained control over the execution lifecycle of concurrent operations.

## Practical Examples of Context Usage

The following examples show how `context.Context` enables better control, observability, and resource management across a variety of real-world scenarios.

### HTTP Server Request Cancellation

Contexts help gracefully handle cancellations when clients disconnect early. Every incoming HTTP request in Go carries a context that gets canceled if the client closes the connection. By checking `<-ctx.Done()`, you can exit early instead of doing unnecessary work:

```
func handler(w http.ResponseWriter, req *http.Request) {
    ctx := req.Context()
    select {
    case <-time.After(5 * time.Second):
        fmt.Fprintln(w, "Response after delay")
    case <-ctx.Done():
        log.Println("Client disconnected")
    }
}
```

In this example, the handler waits for either a simulated delay or cancellation. If the client closes the connection before the timeout, `ctx.Done()` is triggered, allowing the handler to clean up without writing a response.

### Database Operations with Timeouts

Contexts provide a straightforward way to enforce timeouts on database queries. Many drivers support `QueryContext` or similar methods that respect cancellation:

```
ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
defer cancel()

rows, err := db.QueryContext(ctx, "SELECT * FROM users")
if err != nil {
    log.Fatal(err)
}
defer rows.Close()
```

In this case, the context is automatically canceled if the database does not respond within two seconds. The query is aborted, and the application doesn’t hang indefinitely. This helps manage resources and avoids cascading failures in high-load environments.

### Propagating Request IDs for Distributed Tracing

Contexts allow passing tracing information across different layers of a distributed system. For example, a request ID generated at the edge can be attached to the context and logged or used throughout the application:

```
func main() {
    ctx := context.WithValue(context.Background(), "requestID", "12345")
    handleRequest(ctx)
}

func handleRequest(ctx context.Context) {
    log.Printf("Handling request with ID: %v", ctx.Value("requestID"))
}
```

In this example, `WithValue` attaches a request ID to the context. The function `handleRequest` retrieves it using `ctx.Value`, enabling consistent logging and observability without modifying function signatures. This approach is common in middleware, logging, and tracing pipelines.

### Concurrent Worker Management

Context provides control over multiple worker goroutines. By using `WithCancel`, you can propagate a stop signal to all workers from a central point:

```
ctx, cancel := context.WithCancel(context.Background())

for i := 0; i < 10; i++ {
    go worker(ctx, i)
}

// Cancel workers after some condition or signal
cancel()
```

Each worker function should check for `<-ctx.Done()` and return immediately when the context is canceled. This keeps the system responsive, avoids dangling goroutines, and allows graceful termination of parallel work.

### Graceful Shutdown in CLI Tools

In command-line applications or long-running background processes, context simplifies OS signal handling and graceful shutdown:

```
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
defer stop()

<-ctx.Done()
fmt.Println("Shutting down...")
```

In this pattern, `signal.NotifyContext` returns a context that is canceled automatically when an interrupt signal (e.g., Ctrl+C) is received. Listening on `<-ctx.Done()` allows the application to perform cleanup and exit gracefully instead of terminating abruptly.

### Streaming and Real-Time Data Pipelines

Context is ideal for coordinating readers in streaming systems like Kafka consumers, WebSocket readers, or custom pub/sub pipelines:

```
func streamData(ctx context.Context, ch <-chan Data) {
    for {
        select {
        case <-ctx.Done():
            return
        case data := <-ch:
            process(data)
        }
    }
}
```

Here, the function processes incoming data from a channel. If the context is canceled (e.g., during shutdown or timeout), the loop breaks and the goroutine exits cleanly. This makes the system more responsive to control signals and easier to manage under load.

### Middleware and Rate Limiting

Contexts are often used in middleware chains to enforce quotas, trace requests, or carry rate-limit decisions between layers. In a typical HTTP stack, middleware can determine whether a request is allowed based on custom logic (e.g., IP-based rate limiting or user quota checks), and attach that decision to the context so that downstream handlers can inspect it.

Here's a simplified example of how that might work:

```
func rateLimitMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Suppose this is the result of some rate-limiting logic
        rateLimited := true // or false depending on logic

        // Embed the result into the context
        ctx := context.WithValue(r.Context(), "rateLimited", rateLimited)

        // Pass the updated context to the next handler
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

In a downstream handler, you might inspect that value like so:

```
func handler(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    if limited, ok := ctx.Value("rateLimited").(bool); ok && limited {
        http.Error(w, "Too many requests", http.StatusTooManyRequests)
        return
    }
    fmt.Fprintln(w, "Request accepted")
}
```

This pattern avoids the need for shared state between middleware and handlers. Instead, the context acts as a lightweight channel for passing metadata between layers of the request pipeline in a safe and composable way.

## Benchmarking Impact

There's usually nothing to benchmark directly in terms of raw performance when using `context.Context`. Its real benefit lies in improving responsiveness, avoiding wasted computation, and enabling clean cancellations. The impact shows up in reduced memory leaks, fewer stuck goroutines, and more predictable resource lifetimes—metrics best observed through real-world profiling and observability tools.

## Best Practices for Context Usage

- Always pass `context.Context` explicitly, typically as the first argument to a function. This makes context propagation transparent and traceable, especially across API boundaries or service layers. Don’t store contexts in struct fields or global variables. Doing so can lead to stale contexts being reused unintentionally and make cancellation logic harder to reason about.
- Use 1 only for request-scoped metadata, not to pass business logic or application state. Overusing context for general-purpose data storage leads to tight coupling and makes testing and tracing harder.
- Check `ctx.Err()` to differentiate between `context.Canceled` and `context.DeadlineExceeded` where needed. This allows your application to respond appropriately—for example, distinguishing between user-initiated cancellation and timeouts.

Following these practices helps keep context usage predictable and idiomatic.
