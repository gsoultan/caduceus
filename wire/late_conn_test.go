// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"net"
	"sync"
	"time"
)

// lateConn is a connection that is slow in the two ways that let a context's
// goroutine fall behind the reader: a read that has timed out takes a moment
// to return, and a deadline in the past takes longer than that to be set. It
// remembers the read deadline set last.
type lateConn struct {
	net.Conn

	mu       sync.Mutex
	deadline time.Time
	once     sync.Once
	// moved is closed once a deadline in the past has been set.
	moved chan struct{}
}

const (
	lateRead     = 10 * time.Millisecond
	lateDeadline = 40 * time.Millisecond
)

func newLateConn(conn net.Conn) *lateConn {
	return &lateConn{Conn: conn, moved: make(chan struct{})}
}

func (c *lateConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if isTimeout(err) {
		time.Sleep(lateRead)
	}
	return n, err
}

func (c *lateConn) SetReadDeadline(deadline time.Time) error {
	past := !deadline.IsZero() && time.Until(deadline) < 0
	if past {
		time.Sleep(lateDeadline)
	}
	err := c.Conn.SetReadDeadline(deadline)
	c.mu.Lock()
	c.deadline = deadline
	c.mu.Unlock()
	if past {
		c.once.Do(func() { close(c.moved) })
	}
	return err
}

// readDeadline returns the read deadline the connection was left with.
func (c *lateConn) readDeadline() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deadline
}

// standIn is a connection as login leaves it, over a transport of the test's
// choosing.
func standIn(netConn net.Conn, config *Config) *Conn {
	return &Conn{
		netConn: netConn,
		config:  config,
		rbuf:    make([]byte, initialReadBuffer),
		params:  map[string]string{},
	}
}
