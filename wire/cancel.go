// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"encoding/binary"
	"io"
)

// CancelRequest asks the server to cancel whatever this connection is running.
// The protocol wants it on a separate connection, which the server closes
// without answering; whether anything was cancelled is only visible on this one,
// as SQLSTATE 57014. It dials as the connection did, through DialFunc where
// that is set. Safe from any goroutine.
func (c *Conn) CancelRequest(ctx context.Context) error {
	network, address := c.config.target()
	conn, err := c.config.dialer()(ctx, network, address)
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	request := binary.BigEndian.AppendUint32(nil, uint32(12+len(c.secret)))
	request = binary.BigEndian.AppendUint32(request, cancelRequestCode)
	request = binary.BigEndian.AppendUint32(request, c.pid)
	if _, err := conn.Write(append(request, c.secret...)); err != nil {
		return err
	}
	// Wait for the server to hang up, so the request has been read by the time
	// this returns.
	_, _ = io.Copy(io.Discard, conn)
	return nil
}
