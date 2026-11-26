// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "context"

// Ping makes a round trip to the server with the cheapest statement there
// is, a comment, and returns what went wrong if anything did. Unlike
// CheckConn it finds a server that vanished without a word, at the cost of
// waiting for it.
func (s *Conn) Ping(ctx context.Context) error {
	_, err := s.exec(ctx, "-- ping", nil)
	return err
}

// CheckConn reports, without a round trip and without waiting, whether the
// connection still looks usable: open, and the server neither gone nor
// having sent an error unasked, as it does when the session is ended for it.
// A pool can call it on every connection it hands out; see wire.Conn's
// CheckConn for what it can and cannot see.
func (s *Conn) CheckConn() error {
	if s.open {
		return ErrConnBusy
	}
	return s.conn.CheckConn()
}
