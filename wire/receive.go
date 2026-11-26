// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
)

// Receive returns the next message, handling the asynchronous ones on the way:
// a ParameterStatus is recorded, a NotificationResponse handed to
// Config.OnNotification or else queued, a notice handed to Config.OnNotice or
// else dropped.
//
// The body aliases the connection's buffer and is valid only until the next
// call. An ErrorResponse is returned as a message, for the caller to decide what
// it ends; use ParseError to read it.
//
// A timeout leaves the connection intact, and the same message is returned by
// the next call. Any other failure closes it.
func (c *Conn) Receive() (byte, []byte, error) {
	for {
		kind, body, err := c.next()
		if err != nil {
			return 0, nil, err
		}
		switch kind {
		case MsgParameterStatus:
			c.recordParameter(body)
		case MsgNotification:
			c.notified(body)
		case MsgNoticeResponse:
			c.noticed(body)
		case MsgReadyForQuery:
			if len(body) != 1 {
				return 0, nil, c.fail(ErrProtocol)
			}
			c.txStatus = body[0]
			c.calmed()
			c.mark.settle()
			return kind, body, nil
		default:
			return kind, body, nil
		}
	}
}

// notified hands a NotificationResponse to OnNotification, or queues it.
func (c *Conn) notified(body []byte) {
	note, ok := parseNotification(body)
	switch {
	case !ok:
	case c.config.OnNotification != nil:
		c.config.OnNotification(note)
	default:
		c.notifications = append(c.notifications, note)
	}
}

// noticed hands a NoticeResponse to OnNotice.
func (c *Conn) noticed(body []byte) {
	if c.config.OnNotice != nil {
		c.config.OnNotice(parseError(body))
	}
}

// ParseError reads the body of an ErrorResponse.
func ParseError(body []byte) *Error {
	return parseError(body)
}

func (c *Conn) recordParameter(body []byte) {
	name, rest, ok := cutString(body)
	if !ok {
		return
	}
	value, _, ok := cutString(rest)
	if ok {
		c.params[name] = value
	}
}

// next reads one whole message into the buffer and consumes it.
func (c *Conn) next() (byte, []byte, error) {
	if c.closed.Load() {
		return 0, nil, ErrClosed
	}
	if err := c.fill(headerSize); err != nil {
		return 0, nil, err
	}
	header := c.rbuf[c.rpos:]
	length := int(binary.BigEndian.Uint32(header[1:5])) - 4
	if length < 0 {
		return 0, nil, c.fail(ErrProtocol)
	}
	if length > c.config.maxMessage() {
		return 0, nil, c.fail(ErrMessageTooLarge)
	}
	if err := c.fill(headerSize + length); err != nil {
		return 0, nil, err
	}
	kind := c.rbuf[c.rpos]
	body := c.rbuf[c.rpos+headerSize : c.rpos+headerSize+length : c.rpos+headerSize+length]
	c.rpos += headerSize + length
	return kind, body, nil
}

// fill makes sure at least n unconsumed bytes are buffered.
func (c *Conn) fill(n int) error {
	if c.rend-c.rpos >= n {
		return nil
	}
	c.makeRoom(n)
	c.used = c.used || c.wide(n)
	for c.rend-c.rpos < n {
		read, err := c.netConn.Read(c.rbuf[c.rend:])
		c.rend += read
		c.took(read)
		// Full, and by a read of at least half of it: more was waiting than
		// there was room for. A buffer the last bytes of a segment happened to
		// fill says nothing of the kind, and a result that trickles in would
		// otherwise grow one it has no use for.
		c.streaming = c.rend == len(c.rbuf) && 2*read >= len(c.rbuf)
		c.used = c.used || c.streamed(read)
		if err == nil {
			continue
		}
		if c.rend-c.rpos >= n {
			return nil
		}
		if isTimeout(err) {
			c.overdue = true
			return err
		}
		return c.fail(err)
	}
	return nil
}

// makeRoom moves the unconsumed bytes to the front of the buffer and grows it
// if n of them would still not fit. A buffer the connection has no more use
// for is let go first.
func (c *Conn) makeRoom(n int) {
	pending := c.rend - c.rpos
	if pending == 0 && n <= initialReadBuffer && c.shrinkable() {
		c.giveBack()
	}
	if c.streaming && len(c.rbuf) < streamReadBuffer && n <= streamReadBuffer {
		n = max(n, 2*len(c.rbuf))
		c.streaming = false
	}
	if len(c.rbuf) < n {
		grown := make([]byte, max(n, 2*len(c.rbuf)))
		copy(grown, c.rbuf[c.rpos:c.rend])
		c.rbuf, c.rpos, c.rend = grown, 0, pending
		// Whatever it grew for, the exchange in hand is using it.
		c.used = true
		return
	}
	if len(c.rbuf)-c.rpos < n || c.rpos == c.rend {
		copy(c.rbuf, c.rbuf[c.rpos:c.rend])
		c.rpos, c.rend = 0, pending
	}
}

// streamed accounts for a read, and reports whether the exchange has by now
// read more than the initial buffer holds, through a buffer no larger than a
// stream grows one. Such an exchange is what that buffer is for, however the
// network cut it into reads. Told by the size of a read instead, a result that
// arrives in segments gives back a buffer it will only grow again.
func (c *Conn) streamed(read int) bool {
	if c.got < initialReadBuffer {
		c.got += read
	}
	return c.got >= initialReadBuffer && len(c.rbuf) <= streamReadBuffer
}

// wide reports whether a message of n bytes is one that a buffer grown past a
// stream's, for a wide message, is the right size for: at least a quarter of
// it. Anything narrower is read as well through what a stream grows, and must
// not hold on to a buffer a single wide row left behind.
func (c *Conn) wide(n int) bool {
	return len(c.rbuf) > streamReadBuffer && 4*n >= len(c.rbuf)
}

// shrinkable reports whether the buffer is larger than this connection needs
// now: grown for one large message, or for results the connection has stopped
// reading and the exchange in hand is not one of. Without that last condition
// a result read after a run of short exchanges has its buffer taken away
// whenever it stands empty, and grows it again with the next read.
func (c *Conn) shrinkable() bool {
	return len(c.rbuf) > shrinkAbove || (c.calm >= calmExchanges && !c.used && len(c.rbuf) > initialReadBuffer)
}

// giveBack replaces an empty buffer with one of the initial size.
func (c *Conn) giveBack() {
	c.rbuf, c.rpos, c.rend = make([]byte, initialReadBuffer), 0, 0
	c.streaming = false
}

// calmed ends an exchange for the read buffer. It counts how many in a row the
// buffer was larger than they needed, and gives back there and then one the
// connection has no more use for: a connection goes back to its pool at the
// end of an exchange and may sit there, so a buffer let go only at the next
// read is held for as long as it sits.
func (c *Conn) calmed() {
	if c.used {
		c.calm = 0
	} else if c.calm < calmExchanges {
		c.calm++
	}
	c.used, c.got = false, 0
	if c.rpos == c.rend && c.shrinkable() {
		c.giveBack()
	}
}

// isTimeout reports whether err is a deadline expiring.
func isTimeout(err error) bool {
	var timeout net.Error
	return errors.As(err, &timeout) && timeout.Timeout()
}

// IsTimeout reports whether err is a read or write deadline expiring, which
// leaves a connection usable.
func IsTimeout(err error) bool {
	return isTimeout(err)
}

// Timeout reports whether err is from something that ran out of time: its
// context's deadline passed, or a read or a write ran into one.
func Timeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || isTimeout(err)
}
