Source: https://goperf.dev/01-common-patterns/immutable-data/

# Immutable Data Sharing

One common source of slowdown in high-performance Go programs is the way shared data is accessed under concurrency. The usual tools—mutexes and channels—work well, but they’re not free. Mutexes can become choke points if many goroutines try to grab the same lock. Channels, while elegant for coordination, can introduce blocking and make control flow harder to reason about. Both require careful use: it’s easy to introduce subtle bugs or unexpected performance issues if synchronization isn’t tight.

A powerful alternative is immutable data sharing. Instead of protecting data with locks, you design your system so that shared data is never mutated after it's created. This minimizes contention and simplifies reasoning about your program.

## Why Immutable Data?

Immutability brings several advantages to concurrent programs:

- No locks needed: Multiple goroutines can safely read immutable data without synchronization.
- Easier reasoning: If data can't change, you avoid entire classes of race conditions.
- Copy-on-write optimizations: You can create new versions of a structure without altering the original, which is useful for config reloading or versioning a state.

## Practical Example: Shared Config

Imagine you have a long-running service that periodically reloads its configuration from a disk or a remote source. Multiple goroutines read this configuration to make decisions.

Here's how immutable data helps:

### Step 1: Define the Config Struct

```
// config.go
type Config struct {
    LogLevel string
    Timeout  time.Duration
    Features map[string]bool // This needs attention!
}
```

### Step 2: Ensure Deep Immutability

Maps and slices in Go are reference types. Even if the Config struct isn't changed, someone could accidentally mutate a shared map. To prevent this, we make defensive copies:

```
func NewConfig(logLevel string, timeout time.Duration, features map[string]bool) *Config {
    copiedFeatures := make(map[string]bool, len(features))
    for k, v := range features {
        copiedFeatures[k] = v
    }

    return &Config{
        LogLevel: logLevel,
        Timeout:  timeout,
        Features: copiedFeatures,
    }
}
```

Now, every config instance is self-contained and safe to share.

### Step 3: Atomic Swapping

Use `atomic.Value` to store and safely update the current config.

```
var currentConfig atomic.Pointer[Config]

func LoadInitialConfig() {
    cfg := NewConfig("info", 5*time.Second, map[string]bool{"beta": true})
    currentConfig.Store(cfg)
}

func GetConfig() *Config {
    return currentConfig.Load()
}
```

Now all goroutines can safely call `GetConfig()` with no locks. When the config is reloaded, you just `Store` a new immutable copy.

### Step 4: Using It in Handlers

```
func handler(w http.ResponseWriter, r *http.Request) {
    cfg := GetConfig()
    if cfg.Features["beta"] {
        // Enable beta path
    }
    // Use cfg.Timeout, cfg.LogLevel, etc.
}
```

## Practical Example: Immutable Routing Table

Suppose you're building a lightweight reverse proxy or API gateway and must route incoming requests based on path or host. The routing table is read thousands of times per second and updated only occasionally (e.g., from a config file or service discovery).

### Step 1: Define Route Structs

```
type Route struct {
    Path    string
    Backend string
}

type RoutingTable struct {
    Routes []Route
}
```

### Step 2: Build Immutable Version

To ensure immutability, we deep-copy the slice of routes when constructing a new routing table.

```
func NewRoutingTable(routes []Route) *RoutingTable {
    copied := make([]Route, len(routes))
    copy(copied, routes)
    return &RoutingTable{Routes: copied}
}
```

### Step 3: Store It Atomically

```
var currentRoutes atomic.Pointer[RoutingTable]

func LoadInitialRoutes() {
    table := NewRoutingTable([]Route{
        {Path: "/api", Backend: "http://api.internal"},
        {Path: "/admin", Backend: "http://admin.internal"},
    })
    currentRoutes.Store(table)
}

func GetRoutingTable() *RoutingTable {
    return currentRoutes.Load()
}
```

### Step 4: Route Requests Concurrently

```
func routeRequest(path string) string {
    table := GetRoutingTable()
    for _, route := range table.Routes {
        if strings.HasPrefix(path, route.Path) {
            return route.Backend
        }
    }
    return ""
}
```

Now, your routing logic can scale safely under load with zero locking overhead.

## Scaling Immutable Routing Tables

As systems grow, routing tables can expand to hundreds or even thousands of entries. While immutability brings clear benefits—safe concurrent access, predictable behavior—it becomes costly if every update means copying the entire structure. At some point, rebuilding the whole table for each minor change doesn’t scale.

To keep immutability without paying for full reconstruction on every update, the design needs to evolve. There are several ways to do this—each preserving the core benefits while reducing overhead.

### Scenario 1: Segmented Routing

Imagine a multi-tenant system where each customer has their own set of routing rules. Instead of one giant slice of routes, you can split them into a map:

```
type MultiTable struct {
    Tables map[string]RoutingTable // key = tenant ID
}
```

If only customer "acme" updates their rules, you clone just that slice and update the map. Then you atomically swap in a new version of the full map. All other tenants continue using their existing, untouched routing tables.

This approach reduces memory pressure and speeds up updates without losing immutability. It also isolates blast radius: a broken rule set in one segment doesn’t affect others.

### Scenario 2: Indexed Routing Table

Let’s say your router matches by exact path, and lookup speed is critical. You can use a `map[string]RouteHandler` as an index:

```
type RouteIndex map[string]RouteHandler
```

When a new path is added, clone the current map, add the new route, and publish the new version. Because maps are shallow, this is fast for moderate numbers of routes. Reads are constant time, and updates are efficient because only a small part of the structure changes.

### Scenario 3: Hybrid Staging and Publishing

Suppose you’re doing a batch update — maybe reading hundreds of routes from a database. Instead of rebuilding live, you keep a mutable staging area:

```
var mu sync.Mutex
var stagingRoutes []Route
```

You load and manipulate data in staging under a mutex, then convert to an immutable `RoutingTable` and store it atomically. This lets you safely prepare complex changes without locking readers or affecting live traffic.

## Benchmarking Impact

Benchmarking immutable data sharing in real-world systems is difficult to do in a generic, meaningful way. Factors like structure size, read/write ratio, and memory layout all heavily influence results.

Rather than presenting artificial benchmarks here, we recommend reviewing the results in the [Atomic Operations and Synchronization Primitives](https://goperf.dev/01-common-patterns/atomic-ops/#benchmarking-impact) article. Those benchmarks clearly illustrate the potential performance benefits of using atomic.Value over traditional synchronization primitives like sync.RWMutex, especially in highly concurrent read scenarios.

## When to Use This Pattern

Immutable data sharing is ideal when:

- The data is read-heavy and write-light (e.g., configuration, feature flags, global mappings). This works well because the cost of creating new immutable versions is amortized over many reads, and avoiding locks provides a performance boost.

- You want to minimize locking without sacrificing safety. By sharing read-only data, you remove the need for mutexes or coordination, reducing the chances of deadlocks or race conditions.

- You can tolerate minor delays between update and read (eventual consistency). Since data updates are not coordinated with readers, there might be a small delay before all goroutines see the new version. If exact timing isn't critical, this tradeoff simplifies your concurrency model.

It’s less suitable when updates must be transactional across multiple pieces of data or happen frequently. In those cases, the cost of repeated copying or lack of coordination can outweigh the benefits.
