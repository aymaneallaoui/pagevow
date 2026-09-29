Source: https://goperf.dev/02-networking/dns_performance/

# Tuning DNS Performance in Go Services

DNS resolution tends to fly under the radar, but it can still slow down Go applications. Even brief delays in lookups can stack up in distributed or microservice architectures where components frequently communicate. Understanding how Go resolves DNS under the hood — and how to adjust it — can make your service more responsive and reliable.

## How DNS Resolution Works in Go: cgo vs. Native Resolver

Go supports two different ways of handling DNS queries: the built-in **pure-Go** resolver and the **cgo-based** resolver.

The `pure-Go` resolver is fully self-contained and avoids using any external DNS libraries. It reads its configuration from `/etc/resolv.conf` (on Unix-like systems) and talks directly to the configured nameservers. This makes it simple and generally performant, though it sometimes struggles to handle more exotic or highly customized DNS environments.

In contrast, the **cgo-based** resolver delegates DNS resolution to the operating system’s own resolver (through the C standard library, `libc`). This gives better compatibility with complicated or custom DNS environments—like those involving LDAP or multicast DNS—but it also comes with a tradeoff. The cgo resolver adds overhead due to calls into external C libraries, and it can sometimes lead to issues around thread safety or unpredictable latency spikes.

It's possible to explicitly configure Go to prefer the **pure-Go** resolver using an environment variable:

```
export GODEBUG=netdns=go
```

Alternatively, force the use of cgo resolver:

```
export GODEBUG=netdns=cgo
```

### Runtime Dependencies

Enabling cgo changes how the Go binary interacts with the operating system. With cgo turned on, the binary no longer stands alone — it links dynamically to `libc` and the system loader, which `ldd` reveals in its output.

```
$ ldd ./app-cgo
    linux-vdso.so.1 (0x0000fa34ddbad000)
    libc.so.6 => /lib/aarch64-linux-gnu/libc.so.6 (0x0000fa34dd9b0000)
    /lib/ld-linux-aarch64.so.1 (0x0000fa34ddb70000)
```

A cgo-enabled binary relies on the system’s C runtime (`libc.so.6`) and the dynamic loader (`ld-linux`). Without these shared libraries available on the host, the binary won’t start — which makes it unsuitable for stripped-down environments like scratch containers.

By contrast, a pure-Go binary is completely self-contained and statically linked. If you run `ldd` on it, you’ll simply see:

```
$ ldd ./app-pure
    not a dynamic executable
```

This shows that all the code the binary needs is already baked in, with no dependency on shared libraries at runtime. Because of this, pure-Go builds are a good fit for minimal containers or bare environments without a C runtime, offering better portability and fewer operational surprises. The downside is that these binaries can’t take advantage of system-level resolver features that require cgo and the host’s `libc`.

## DNS Caching: Why and When

Caching DNS results prevents the application from sending the same queries over and over, which can eliminate a noticeable amount of latency. Each lookup incurs at least one network round-trip, and when this happens at scale, the cumulative cost can become a serious drag on service performance.

Many operating systems and hosting environments already include some form of DNS caching. On Windows, the `DNS Client` service keeps a local cache; on macOS, `mDNSResponder` handles this; and on most Linux systems, `systemd-resolved` or `nscd` often provides a caching layer. In cloud environments, DNS queries are often routed through a nearby caching resolver inside the same data center. These mechanisms do help reduce latency, but they operate transparently to the application and offer little visibility or control over TTLs and policies. On Linux, if `systemd-resolved` isn’t enabled, no caching happens at all unless you configure it explicitly.

Since Go itself doesn’t include any DNS caching, you have to implement it yourself if you want fine-grained, application-level control. One option is to run your own caching DNS server nearby; another is to embed a lightweight cache directly in your code, using a third-party library.

Example using [go-cache](https://github.com/patrickmn/go-cache) for a simple DNS cache:

```
import (
    "github.com/patrickmn/go-cache"
    "net"
    "time"
)

var dnsCache = cache.New(5*time.Minute, 10*time.Minute)

func LookupWithCache(host string) ([]net.IP, error) {
    if cachedIPs, found := dnsCache.Get(host); found {
        return cachedIPs.([]net.IP), nil
    }

    ips, err := net.LookupIP(host)
    if err != nil {
        return nil, err
    }
    dnsCache.Set(host, ips, cache.DefaultExpiration)
    return ips, nil
}
```

Overdoing DNS caching has its downsides — it can leave you serving stale records and make your service fragile if upstream addresses change. It’s worth tuning your cache expiration times so they reflect how often the domains you rely on actually change.

## Using Custom Dialers and Pre-resolved IPs

In latency-sensitive services, it often makes sense to resolve DNS up front or use a custom dialer to control resolution explicitly. Every call to `net.Dial` or `net.DialContext` with a hostname triggers a lookup, which can involve syscalls, context switches, and sometimes even a network round-trip if the cache is cold. At high throughput, this overhead adds up.

To eliminate this overhead, you can resolve hostnames during startup and save the resulting IPs. This approach is particularly effective when dealing with a fixed set of endpoints that rarely change.

```
var serviceAddr string

func init() {
    ips, err := net.LookupIP("api.example.com")
    if err != nil || len(ips) == 0 {
        panic("Unable to resolve api.example.com")
    }
    serviceAddr = ips[0].String() // in real code, consider picking an IP randomly, prefer IPv4 if needed, or iterate over ips with checks as appropriate
}
```

One drawback is that it fixes the IP for the lifetime of the process, so if the endpoint changes its address, connections may start failing. To handle this gracefully, you can run a background goroutine that refreshes the resolved IP at regular intervals.

Custom dialers take it one step further: they allow you to control how DNS resolution and socket establishment occur on a per-connection basis. This will enable you to force connections through a specific resolver, hardwire pre-resolved IP addresses, or even bypass DNS completely by dialing IP addresses directly.

```
import (
    "net"
    "context"
    "time"
)

var dialer = &net.Dialer{
    Timeout:   5 * time.Second,
    KeepAlive: 30 * time.Second,
    Resolver: &net.Resolver{
        PreferGo: true,
        Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
            return net.Dial(network, "8.8.8.8:53")
        },
    },
}

func ConnectWithCustomDialer(ctx context.Context, address string) (net.Conn, error) {
    return dialer.DialContext(ctx, "tcp", address)
}
```

Custom dialers can also bypass unreliable system resolvers or direct DNS queries through a dedicated, faster nameserver. They give you precise control over how resolution happens, but that control comes with added complexity — you’ll need to handle fallback and refresh logic yourself.

## Metrics and Debugging Real-world DNS Slowdowns

Identifying and troubleshooting DNS-induced latency requires insightful metrics and targeted debugging techniques. Measuring DNS is not just about seeing how fast it is — it helps answer deeper questions: are lookups hitting OS or provider caches? is the network path to the DNS server flaky? are specific nameservers slower than others? are IPv6 and IPv4 behaving differently? Without visibility, DNS issues can silently degrade performance and reliability.

### Metrics

Make sure to track DNS resolution times in your service metrics so you can see how much time lookups actually add to each request. The simplest way is to wrap your DNS calls with a timer that starts just before the lookup and records the duration after it completes. Over time, these measurements help you identify trends, spot intermittent slowness, and correlate DNS delays with other parts of your system.

```
start := time.Now()
ips, err := net.LookupIP("example.com")
duration := time.Since(start)

recordDNSLookupDuration("example.com", duration)
```

Looking at high-percentile latencies — like the 95th or 99th — can reveal sporadic slowdowns or flaky DNS behavior that averages might hide.

### Debugging Tips

When facing unexplained latency spikes, leverage Go’s built-in debug mode:

```
export GODEBUG=netdns=2
```

Enabling this produces detailed DNS query logs that show exactly how each request is handled from start to finish. You can see when a server responds slowly, when lookups fail and trigger retries, or when the runtime unexpectedly falls back to the cgo resolver. Such detailed insight makes it much easier to pinpoint elusive DNS problems that standard metrics often miss.

### Advanced DNS Performance Tips

Running a local DNS caching resolver, such as `dnsmasq` or `Unbound`, close to your service can eliminate the extra latency of external lookups. If security and privacy are concerns, enabling DNS-over-HTTPS (DoH) or DNS-over-TLS (DoT) is also an option, though it comes with some additional latency due to encryption. Finally, reviewing and tuning your `/etc/resolv.conf` — adjusting retry counts and timeout settings — helps ensure the resolver behaves predictably under load or failure conditions.
