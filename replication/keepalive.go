// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "time"

// Keepalive is a PrimaryKeepalive message.
type Keepalive struct {
	// ServerEnd is the end of the WAL on the server.
	ServerEnd LSN
	// ServerTime is the server's clock when it was sent.
	ServerTime time.Time
	// ReplyRequested asks for a standby status update straight away; the
	// server disconnects a client that ignores it past wal_sender_timeout.
	ReplyRequested bool
}

// ParseKeepalive parses a CopyData body whose first byte is PrimaryKeepalive.
func ParseKeepalive(body []byte) (Keepalive, error) {
	if len(body) == 0 || body[0] != PrimaryKeepalive {
		return Keepalive{}, ErrMalformed
	}
	r := reader{data: body[1:]}
	k := Keepalive{ServerEnd: LSN(r.uint64()), ServerTime: r.timestamp(), ReplyRequested: r.byte() == 1}
	if r.err != nil {
		return Keepalive{}, r.err
	}
	return k, nil
}
