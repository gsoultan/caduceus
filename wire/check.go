// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import "time"

// checkWait bounds how long CheckConn reads what the server sent unasked.
const checkWait = 10 * time.Millisecond

// CheckConn reports, without a round trip and without waiting on the
// server, whether the connection still looks usable between exchanges. It
// looks at the socket without reading it: a server that hung up, or one that
// sent a FATAL error unasked, as it does when an administrator ends the
// session or the server shuts down, is found, and the connection closed and
// the error returned. A notification, a notice or a parameter's new value
// that arrived while it was idle is handled as Receive would.
//
// It cannot see a connection whose peer vanished without a word, which only
// a statement, or TCP keepalives, can find. Off Linux, macOS and the BSDs it
// can only see a connection already closed.
func (c *Conn) CheckConn() error {
	if c.closed.Load() {
		return ErrClosed
	}
	waiting, err := c.peek()
	if err != nil {
		return c.fail(err)
	}
	if !waiting && c.rend == c.rpos {
		return nil
	}
	return c.readUnasked()
}

// readUnasked reads what the server sent between exchanges.
func (c *Conn) readUnasked() error {
	_ = c.netConn.SetReadDeadline(time.Now().Add(checkWait))
	defer c.netConn.SetReadDeadline(time.Time{})
	for {
		if err := c.unasked(); err != nil {
			return c.fail(err)
		}
		waiting, err := c.peek()
		if err != nil {
			return c.fail(err)
		}
		if !waiting && c.rend == c.rpos {
			return nil
		}
	}
}

// unasked reads one message the server sent unasked and handles it. An
// error, or a message of any other kind, is returned as an error: nothing
// else is the server's to send between exchanges.
func (c *Conn) unasked() error {
	kind, body, err := c.next()
	if err != nil {
		return err
	}
	switch kind {
	case MsgParameterStatus:
		c.recordParameter(body)
	case MsgNotification:
		c.notified(body)
	case MsgNoticeResponse:
		c.noticed(body)
	case MsgErrorResponse:
		return parseError(body)
	default:
		return ErrProtocol
	}
	return nil
}
