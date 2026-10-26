package manager

import (
	"context"
	"net"
	"sync"
	"time"
)

const postgresResponseTimeout = 5 * time.Second

type postgresDialer struct{}

// Dial supplies the driver's legacy dial interface with the same finite profile.
func (d postgresDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

// DialTimeout preserves an earlier timeout requested by the driver.
func (d postgresDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return d.DialContext(ctx, network, address)
}

// DialContext wraps the actual original socket, including cancellation connections.
func (postgresDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return &postgresDeadlineConn{Conn: conn}, nil
}

// The pinned driver writes requests and clears its startup deadline here.
// Reads never renew the response budget, including a trickled response.
type postgresDeadlineConn struct {
	net.Conn
	mu             sync.Mutex
	driverDeadline time.Time
}

// SetDeadline preserves the driver's startup cap without permitting an infinite clear.
func (c *postgresDeadlineConn) SetDeadline(deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.driverDeadline = deadline
	return c.boundDeadline()
}

// Caller holds mu only during deadline state/update, never during socket I/O.
func (c *postgresDeadlineConn) boundDeadline() error {
	deadline := time.Now().Add(postgresResponseTimeout)
	if !c.driverDeadline.IsZero() && c.driverDeadline.Before(deadline) {
		deadline = c.driverDeadline
	}
	return c.Conn.SetDeadline(deadline)
}

// Write renews the response budget before sending a request, even after idle expiry.
func (c *postgresDeadlineConn) Write(data []byte) (int, error) {
	c.mu.Lock()
	err := c.boundDeadline()
	c.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return c.Conn.Write(data)
}
