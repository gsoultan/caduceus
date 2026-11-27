// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// copyIn sends the data of a COPY FROM STDIN from a reader. Once more than
// copyChunk has gone out its replies are read while it writes, so a server
// answering rows with notices is never left waiting to be read; up to that
// much the server's socket takes whole, and the replies are read afterwards.
type copyIn struct {
	session *Conn
	replies *copyReplies
	sent    int
}

func (c *copyIn) send(buf []byte) error {
	if c.replies == nil && c.sent+len(buf) > copyChunk {
		c.replies = c.session.readCopyReplies(1)
	}
	c.sent += len(buf)
	return c.session.conn.Send(buf)
}

func (c *copyIn) refused() bool { return c.replies != nil && c.replies.failed.Load() }

// finish waits for the replies being read and returns the COPY's tag and the
// first of its errors.
func (c *copyIn) finish(readErr, sendErr, failure error) (string, error) {
	_, tag, refused, broken := c.replies.wait(1)
	switch {
	case broken != nil:
		return "", broken
	case sendErr != nil:
		return "", sendErr
	}
	return tag, firstError(failure, firstError(readErr, refused))
}
