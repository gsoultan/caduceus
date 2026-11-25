// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"errors"

	"github.com/gsoultan/caduceus/wire"
)

// A transaction's BEGIN is not sent when Begin is called. It waits in
// Conn.pending and travels ahead of the transaction's first statement, in the
// same write, so a transaction costs one round trip fewer: two for BEGIN,
// one statement, COMMIT, where three were sent before. PostgreSQL takes no
// snapshot at BEGIN, so what the transaction sees is unchanged; only
// transaction_timestamp() moves, to when the first statement arrives.
//
// The statement behind a BEGIN must not run if the BEGIN is refused, as a
// serializable one is on a hot standby: it would run on its own and commit.
// So the two always share one fate, and nothing else is sent until the server
// has answered for both. Over the extended protocol BEGIN is in the
// statement's own Sync, and an error makes the server skip to it; over the
// simple protocol it is in the statement's own Query string. A batch is one
// Sync from end to end and takes it the same way, and so does whatever has to
// be described first, for a batch, a pipeline or a copy. Only a pipeline of
// statements all known already sends BEGIN by itself: its windows each end in
// a Sync and are sent ahead of the answers.
//
// A refused BEGIN leaves the server with no transaction and the caller
// believing it has one. Until the caller has settled it, with Commit, Rollback
// or Unwind, the connection runs nothing at all: Conn.refused is the
// connection's state, not the Tx's, because a statement run on the connection
// directly would otherwise run on its own.

// txStatus is the transaction status as a caller should see it, which is the
// server's except for a transaction begun here. That one is open before its
// BEGIN has been sent and while the reply to it is still to be read, and has
// failed once the server has refused the BEGIN: nothing may run until it is
// settled, exactly as in a transaction the server holds failed.
func (s *Conn) txStatus() byte {
	switch {
	case s.refused:
		return wire.TxFailed
	case s.pending != "" || s.prefixed:
		return wire.TxActive
	}
	return s.conn.TxStatus()
}

// appendBegin appends the pending BEGIN, parsed into the unnamed statement and
// run, for the caller to follow with a statement and a Sync. Call began once
// the messages are certain to be sent.
func (s *Conn) appendBegin(buf []byte) []byte {
	if s.pending == "" {
		return buf
	}
	buf = wire.AppendParse(buf, "", s.pending, nil)
	buf, mark := wire.StartMessage(buf, 'B')
	// Unnamed portal, unnamed statement, no parameter formats, no parameters,
	// no result formats.
	buf = append(buf, 0, 0, 0, 0, 0, 0, 0, 0)
	buf = wire.FinishMessage(buf, mark)
	return wire.AppendExecute(buf, "", 0)
}

// began records that the pending BEGIN is in the messages about to be sent,
// so its replies are to be taken out of what the statement reads.
func (s *Conn) began() {
	if s.pending != "" {
		s.pending, s.prefixed = "", true
	}
}

// simpleBehindBegin runs sql over the simple protocol behind the pending
// BEGIN, in the one Query string that makes the two a single round trip.
//
// The server parses a Query string whole before it runs any of it, so a
// statement it cannot parse takes the BEGIN down with it, where behind a BEGIN
// sent ahead it would have failed inside a transaction already open. The
// caller must not be able to tell: one leaves a failed transaction for
// Rollback to end, the other a session with no transaction at all. So a string
// the server refused before running any of it is sent again as its two parts.
// If the server will not have the BEGIN alone either, the transaction is
// refused; otherwise it is open, and the statement fails in it as it would
// have behind a BEGIN sent ahead.
func (s *Conn) simpleBehindBegin(sql string) (string, error) {
	begin := s.pending
	if begin == "" {
		return s.simple(sql)
	}
	s.began()
	tag, err := s.simple(begin + "; " + sql)
	if !s.refused || !isServerError(err) || s.conn.IsClosed() {
		// The position in a server error is counted from the BEGIN put in front.
		return tag, unshift(err, int32(len(begin)+2))
	}
	s.refused = false
	if _, err := s.simple(begin); err != nil {
		s.refused = isServerError(err)
		return "", err
	}
	return s.simple(sql)
}

// flushBegin sends the pending BEGIN on its own and waits for it, for an
// exchange that cannot wait for the answer to its first Sync.
func (s *Conn) flushBegin() error {
	if s.pending == "" {
		return nil
	}
	sql := s.pending
	s.pending = ""
	_, err := s.simple(sql)
	s.refused = err != nil && isServerError(err)
	return err
}

// receive is the connection's Receive with the replies to a BEGIN that went
// ahead of the statement taken out.
func (s *Conn) receive() (byte, []byte, error) {
	kind, body, err := s.conn.Receive()
	if !s.prefixed || err != nil {
		return kind, body, err
	}
	return s.afterBegin(kind, body)
}

// afterBegin reads past BEGIN's ParseComplete, BindComplete and
// CommandComplete, and the CloseComplete of every evicted statement closed in
// front of it. An error in their place is the server refusing BEGIN, or, over
// the simple protocol, refusing to parse the string it shares with its
// statement: either way neither ran, and the transaction never opened.
func (s *Conn) afterBegin(kind byte, body []byte) (byte, []byte, error) {
	for {
		switch kind {
		case wire.MsgCloseComplete, wire.MsgParseComplete, wire.MsgBindComplete:
		case wire.MsgCommandComplete:
			s.prefixed = false
		default:
			s.prefixed = false
			s.refused = kind == wire.MsgErrorResponse
			return kind, body, nil
		}
		var err error
		if kind, body, err = s.conn.Receive(); err != nil || !s.prefixed {
			return kind, body, err
		}
	}
}

// unshift takes the BEGIN in front of a statement back out of the position a
// server error reports.
func unshift(err error, added int32) error {
	if added == 0 || err == nil {
		// Nearly every call. What errors.As is handed must live on the heap,
		// so it is not declared until an error needs it.
		return err
	}
	var failure *wire.Error
	if errors.As(err, &failure) && failure.Position > added {
		failure.Position -= added
	}
	return err
}
