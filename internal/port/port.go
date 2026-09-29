// Package port checks local TCP ports.
package port

import (
	"context"
	"net"
	"strconv"
	"time"
)

const dialTimeout = 500 * time.Millisecond

// InUse reports whether something accepts connections on 127.0.0.1 at port.
func InUse(ctx context.Context, port int) bool {
	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
