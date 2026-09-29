Source: https://goperf.dev/blog/2025/04/03/lazy-initialization-in-go-using-atomics/

# Lazy initialization in Go using atomics

Aside from the main performance guide, I'm considering using the blog to share quick, informal insights and quirks related to Go performance and optimizations. Let's see if this casual experiment survives contact with reality.

Someone recently pointed out that my `getResource()` function using atomics has a race condition. Guilty as charged—rookie mistake, really. The issue? I naïvely set the `initialized` flag to `true` before the actual resource is ready. Brilliant move, right? This means that with concurrent calls, one goroutine might proudly claim victory while handing out a half-baked resource:

```
var initialized atomic.Bool
var resource *MyResource

func getResource() *MyResource {
    if !initialized.Load() {
        if initialized.CompareAndSwap(false, true) {
            resource = expensiveInit()
        }
    }
    return resource
}
```

Can this mess be salvaged? Almost certainly, it just needs a touch more thought. To squash the race, we need atomic operations directly on the pointer rather than messing with a separate boolean. Enter Go's atomic package with `unsafe.Pointer` magic:

```
import (
    "sync/atomic"
    "unsafe"
)

var resource unsafe.Pointer // holds *MyResource

func getResource() *MyResource {
    // Attempt to load the resource atomically.
    ptr := atomic.LoadPointer(&resource)
    if ptr != nil {
        return (*MyResource)(ptr) // Resource already initialized, return it
    }

    // Resource appears uninitialized, perform expensive initialization
    newRes := expensiveInit()

    // Attempt to atomically set the resource to the newly initialized value
    if atomic.CompareAndSwapPointer(&resource, nil, unsafe.Pointer(newRes)) {
        return newRes // Successfully initialized and stored
    }

    // Another goroutine beat us to initialization, return their initialized resource
    return (*MyResource)(atomic.LoadPointer(&resource))
}
```

This does the trick—but introduces another subtle hiccup: several goroutines might simultaneously invoke `expensiveInit()` if they concurrently see a `nil` pointer. You definetly don't want multiple expensive initializations—unless you're swimming in CPU cycles.

So, yes, we do need state tracking. The obvious fix? An intermediate initialization state:

```
import (
    "runtime"
    "sync/atomic"
    "unsafe"
)

var resource unsafe.Pointer
var initStatus int32 // 0: untouched, 1: in-progress, 2: done

func getResource() *MyResource {
    // Check quickly if initialization is already done
    if atomic.LoadInt32(&initStatus) == 2 {
        return (*MyResource)(atomic.LoadPointer(&resource)) // Initialization complete
    }

    // Attempt to become the goroutine that performs initialization
    if atomic.CompareAndSwapInt32(&initStatus, 0, 1) {
        newRes := expensiveInit() // Only this goroutine initializes
        atomic.StorePointer(&resource, unsafe.Pointer(newRes)) // Store the initialized resource
        atomic.StoreInt32(&initStatus, 2) // Mark initialization as complete
        return newRes
    }

    // Other goroutines wait until initialization completes
    for atomic.LoadInt32(&initStatus) != 2 {
        runtime.Gosched() // Chill out and let the initializer finish
    }
    return (*MyResource)(atomic.LoadPointer(&resource)) // Initialization complete, return resource
}
```

With this approach, only one goroutine earns the privilege of performing `expensiveInit()`. Others politely wait, spinning their wheels (well, yielding the CPU politely) until initialization completes.

Warning

If `expensiveInit()` panics, this implementation will spin forever! Either handle panic properly or ensure that `expensiveInit()` has never panicked.

Info

It's worth noting that this atomic-based approach can be advantageous in scenarios involving a high frequency of calls, where a spinlock's short waiting cycles may be more efficient than a mutex. This is because mutexes can cause frequent context switches, handing control over to the OS scheduler, which can introduce additional overhead.

Of course, the more practical solution is usually simpler—`sync.Once` to the rescue:

```
import "sync"

var (
    resource *MyResource
    once     sync.Once
)

func getResource() *MyResource {
    once.Do(func() {
        resource = expensiveInit()
    })
    return resource
}
```

`sync.Once` elegantly handles initialization, saves your CPUs from unnecessary spin cycles, and keeps your code clean. So, stick to the tried and true unless you have very specific reasons to juggle atomics. Trust me—your future self will thank you.
