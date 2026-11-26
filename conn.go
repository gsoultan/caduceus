// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"strconv"

	"github.com/gsoultan/caduceus/codec"
	"github.com/gsoultan/caduceus/wire"
)

// Conn runs queries on one connection over the extended protocol.
//
// Every exchange — a query and its result, a batch, a copy — starts with begin
// and ends with finish, and at most one is open at a time. One left open is
// what makes a connection busy, and a busy connection is never pooled.
type Conn struct {
	conn *wire.Conn

	// cache holds the statements the connection knows. unnamed is a connection
	// that keeps none on the server: the cache then holds only what the server
	// said of each, and a statement is parsed into the unnamed statement in
	// every Sync that runs it, so that nothing on the server outlives a Sync.
	cache   *statementCache
	unnamed bool
	// closing are evicted statements still to be closed on the server. They are
	// sent ahead of the next Parse, so eviction costs no round trip of its own.
	closing []string
	named   uint64
	// prepared holds the statements the caller prepared, by the name they run
	// by. They are kept apart from the cache, which can neither evict them nor
	// give a statement of its own a name of theirs.
	prepared map[string]*statement

	open  bool
	guard wire.Guard

	// pending is the BEGIN of a transaction begun here and not yet sent; it
	// goes with the transaction's first statement. prefixed is that BEGIN sent
	// and its replies not yet read, and refused a BEGIN that did not take
	// effect, whose transaction must run nothing. epoch counts the
	// transactions begun here, which is how a Tx knows it is still the
	// connection's current one.
	pending  string
	prefixed bool
	refused  bool
	epoch    uint64

	// lastTag is the previous command tag, reused when the next one has the
	// same text, as it does for every run of the same statement.
	lastTag string
}

// begin opens an exchange guarded by ctx.
func (s *Conn) begin(ctx context.Context) error {
	if s.open {
		return ErrConnBusy
	}
	if s.conn.IsClosed() {
		return wire.ErrClosed
	}
	if s.refused {
		// The server has no transaction open and the caller believes it is in
		// one: whatever ran now would run on its own and stay committed.
		return ErrTxNotBegun
	}
	s.guard = s.conn.Watch(ctx)
	s.open = true
	return nil
}

// finish closes the exchange, given how it went, and returns how it went once
// the context has had its say.
func (s *Conn) finish(err error) error {
	if !s.open {
		return err
	}
	s.open = false
	return s.guard.Done(err)
}

// busy reports whether an exchange is open: a result not read to the end.
func (s *Conn) busy() bool { return s.open }

// exec runs a statement and discards its rows. Without arguments it goes over
// the simple protocol, which is one round trip and allows several statements
// separated by semicolons, as a migration script has, unless sql names a
// statement the caller prepared, which is bound and executed.
func (s *Conn) exec(ctx context.Context, sql string, args []any) (string, error) {
	sql, args, err := s.rewritten(ctx, sql, args)
	if err != nil {
		return "", err
	}
	if err := s.begin(ctx); err != nil {
		return "", err
	}
	if len(args) == 0 && s.prepared[sql] == nil {
		tag, err := s.simpleBehindBegin(sql)
		return tag, s.finish(err)
	}
	stmt, err := s.prepare(sql)
	if err == nil {
		err = s.bind(stmt, args)
	}
	if err != nil {
		return "", s.finish(err)
	}
	tag, err := s.drain(stmt)
	return tag, s.finish(err)
}

// query runs a statement and returns its rows. The exchange stays open until
// the cursor has been read to the end or closed.
func (s *Conn) query(ctx context.Context, sql string, args []any) (*Rows, error) {
	c := new(Rows)
	if err := s.queryInto(ctx, sql, args, c); err != nil {
		return nil, err
	}
	return c, nil
}

// queryInto is query with the cursor supplied by the caller, so one that
// embeds it saves an allocation.
func (s *Conn) queryInto(ctx context.Context, sql string, args []any, c *Rows) error {
	sql, args, err := s.rewritten(ctx, sql, args)
	if err != nil {
		return err
	}
	if err := s.begin(ctx); err != nil {
		return err
	}
	stmt, err := s.prepare(sql)
	if err == nil {
		err = s.bind(stmt, args)
	}
	if err != nil {
		return s.finish(err)
	}
	c.init(s, stmt, false)
	return nil
}

// simple sends sql over the simple protocol and reads to ReadyForQuery,
// returning the last command tag.
func (s *Conn) simple(sql string) (string, error) {
	if err := s.conn.Send(wire.AppendQuery(s.conn.Buffer(), sql)); err != nil {
		return "", err
	}
	var tag string
	var failure error
	for {
		kind, body, err := s.receive()
		if err != nil {
			return "", err
		}
		switch kind {
		case wire.MsgCommandComplete:
			tag = s.tag(body)
		case wire.MsgErrorResponse:
			if failure == nil {
				failure = wire.ParseError(body)
			}
		case wire.MsgCopyInResponse:
			if err := s.conn.Send(wire.AppendCopyFail(s.conn.Buffer(), "COPY FROM STDIN needs CopyFrom")); err != nil {
				return "", err
			}
		case wire.MsgCopyOutResponse:
			failure = errors.Join(failure, errCopyOut)
		case wire.MsgReadyForQuery:
			return tag, failure
		}
	}
}

var errCopyOut = errors.New("caduceus: COPY TO STDOUT is not supported through Exec")

// prepare returns the statement for sql, asking the server what it takes and
// returns unless the cache already knows: one round trip on a miss and none on
// a hit. A statement that is kept on the server is parsed there by the same
// round trip. sql may also be the name of a statement the caller prepared.
func (s *Conn) prepare(sql string) (*statement, error) {
	if stmt := s.known(sql); stmt != nil {
		return stmt, nil
	}
	stmt, err := s.parse(s.nextName(), sql)
	if err != nil {
		return nil, err
	}
	s.remember(stmt)
	return stmt, nil
}

// parse parses and describes sql as the statement name, behind the closes
// queued and a transaction's BEGIN still waiting, and reads what the server
// says of it.
func (s *Conn) parse(name, sql string) (*statement, error) {
	buf := s.appendBegin(s.closeEvicted(s.conn.Buffer()))
	buf = wire.AppendParse(buf, name, sql, nil)
	buf = wire.AppendDescribe(buf, 'S', name)
	buf = wire.AppendSync(buf)
	s.began()
	if err := s.conn.Send(buf); err != nil {
		return nil, err
	}
	return s.readDescription(name, sql)
}

// nextName returns the name for a statement about to be parsed: a new one, or
// none on a connection that keeps no statements on the server.
func (s *Conn) nextName() string {
	if s.unnamed {
		return ""
	}
	s.named++
	return "caduceus_" + strconv.FormatUint(s.named, 36)
}

// remember caches a statement. The one that makes room for it is queued to be
// closed on the server, if it is kept there.
func (s *Conn) remember(stmt *statement) {
	if evicted := s.cache.put(stmt); evicted != nil && evicted.name != "" {
		s.closing = append(s.closing, evicted.name)
	}
}

// forget drops a statement from the cache, and queues it to be closed on the
// server if it is kept there.
func (s *Conn) forget(stmt *statement) {
	s.cache.remove(stmt)
	if stmt.name != "" {
		s.closing = append(s.closing, stmt.name)
	}
}

// closeEvicted appends a Close for every evicted statement. They go in front
// of everything else in their Sync, a transaction's BEGIN included: the server
// skips whatever follows an error, and a Close skipped is a statement left
// prepared there for the life of the connection.
func (s *Conn) closeEvicted(buf []byte) []byte {
	for _, name := range s.closing {
		buf = wire.AppendClose(buf, 'S', name)
	}
	s.closing = s.closing[:0]
	return buf
}

// readDescription reads the replies to Parse and Describe up to ReadyForQuery.
func (s *Conn) readDescription(name, sql string) (*statement, error) {
	var failure error
	var params []uint32
	var fields []wire.Field
	for {
		kind, body, err := s.receive()
		if err != nil {
			return nil, err
		}
		switch kind {
		case wire.MsgParameterDescription:
			params, err = wire.ParseParameterDescription(nil, body)
		case wire.MsgRowDescription:
			fields, err = wire.ParseRowDescription(nil, body)
		case wire.MsgErrorResponse:
			if failure == nil {
				failure = wire.ParseError(body)
			}
		case wire.MsgReadyForQuery:
			if failure != nil {
				return nil, failure
			}
			return newStatement(name, sql, params, fields), nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// bind sends Bind, Execute and Sync for a statement, behind its Parse if it is
// not kept on the server.
func (s *Conn) bind(stmt *statement, args []any) error {
	buf, err := s.appendBound(appendParsed(s.appendBegin(s.conn.Buffer()), stmt), stmt, args)
	if err != nil {
		return err
	}
	buf = wire.AppendExecute(buf, "", 0)
	s.began()
	return s.conn.Send(wire.AppendSync(buf))
}

// appendParsed appends what a statement that is not kept on the server needs
// in front of its Bind, and nothing for one that is.
//
// Such a statement was described in an earlier Sync, and a pooler in
// transaction mode may have given that Sync another server connection than
// this one gets: binding the unnamed statement left there would bind whatever
// some other client parsed last on this one. So it is parsed again here, in
// the Sync that binds it, with the parameter types it was described with,
// which makes the server read the arguments as they were encoded. The Describe
// has the server say what its columns are now, for the reader to hold against
// what was remembered.
func appendParsed(buf []byte, stmt *statement) []byte {
	if stmt.name != "" {
		return buf
	}
	buf = wire.AppendParse(buf, "", stmt.sql, stmt.params)
	return wire.AppendDescribe(buf, 'S', "")
}

// appendBound is appendBind for a statement of this connection. Arguments that
// cannot be encoded for the types remembered of a statement that is parsed
// every time may be right for the types it has now, so what is remembered is
// dropped, and the next use asks afresh.
func (s *Conn) appendBound(buf []byte, stmt *statement, args []any) ([]byte, error) {
	buf, err := appendBind(buf, stmt, args)
	if _, miscounted := err.(*ArgumentCountError); err != nil && !miscounted && stmt.name == "" {
		s.cache.remove(stmt)
	}
	return buf, err
}

// appendBind encodes a Bind of the unnamed portal to stmt. Each argument is
// encoded for the type the server resolved for its parameter, in binary where
// the codec can, and the format codes are written once the encoding has chosen
// them.
func appendBind(buf []byte, stmt *statement, args []any) ([]byte, error) {
	if len(args) != len(stmt.params) {
		return buf, &ArgumentCountError{Want: len(stmt.params), Got: len(args)}
	}
	buf, mark := wire.StartMessage(buf, 'B')
	buf = wire.AppendString(wire.AppendString(buf, ""), stmt.name)
	buf = wire.AppendInt16(buf, int16(len(args)))
	formats := len(buf)
	buf = append(buf, make([]byte, 2*len(args))...)
	buf = wire.AppendInt16(buf, int16(len(args)))
	for i, arg := range args {
		length := len(buf)
		buf = append(buf, 0, 0, 0, 0)
		encoded, format, null, err := codec.Encode(buf, stmt.params[i], arg)
		if err != nil {
			return buf, &ArgumentError{Index: i, Err: err}
		}
		buf = encoded
		buf[formats+2*i], buf[formats+2*i+1] = 0, byte(format)
		size := uint32(len(buf) - length - 4)
		if null {
			size = 0xFFFFFFFF
		}
		buf[length], buf[length+1], buf[length+2], buf[length+3] = byte(size>>24), byte(size>>16), byte(size>>8), byte(size)
	}
	buf = wire.AppendInt16(buf, int16(len(stmt.formats)))
	for _, format := range stmt.formats {
		buf = wire.AppendInt16(buf, format)
	}
	return wire.FinishMessage(buf, mark), nil
}

// drain reads a statement's result to ReadyForQuery, discarding rows, and
// returns its command tag.
func (s *Conn) drain(stmt *statement) (string, error) {
	var tag string
	var failure error
	bound := false
	for {
		kind, body, err := s.receive()
		if err != nil {
			return "", err
		}
		switch kind {
		case wire.MsgCommandComplete:
			tag = s.tag(body)
		case wire.MsgRowDescription, wire.MsgNoData:
			// Its rows are not read, but a statement whose columns are not
			// what was remembered is still one to ask about afresh.
			if stmt != nil && !describes(stmt, kind, body) {
				s.cache.remove(stmt)
			}
		case wire.MsgBindComplete:
			bound = true
		case wire.MsgErrorResponse:
			if failure == nil {
				failure = s.serverError(stmt, body, bound)
			}
		case wire.MsgReadyForQuery:
			return tag, failure
		}
	}
}

// serverError parses an ErrorResponse and forgets a statement the error says
// is out of date, so that the next use asks afresh. bound is whether the
// statement's Bind had been answered before the error came. An error that came
// in the place of a BEGIN's replies is the BEGIN's, and says nothing of the
// statement behind it, which never ran.
func (s *Conn) serverError(stmt *statement, body []byte, bound bool) error {
	failure := wire.ParseError(body)
	if stmt != nil && !s.refused && outdated(stmt, failure.Code, bound) {
		s.outdate(stmt)
	}
	return failure
}

// outdate forgets a statement the server says no longer holds, or, for one
// the caller prepared, which only the caller closes, marks it stale, so that
// preparing it again parses it afresh.
func (s *Conn) outdate(stmt *statement) {
	if stmt.prepared {
		stmt.stale = true
		return
	}
	s.forget(stmt)
}

// broken closes a connection whose stream no longer says what was expected of
// it, and returns the error for that. The rest of what the server is sending
// would otherwise be read by the next exchange as its own.
func (s *Conn) broken() error {
	_ = s.conn.Close(context.Background())
	return wire.ErrProtocol
}

// outdated reports whether an error says what is cached of a statement no
// longer holds. bound is whether the statement's Bind had been answered before
// the error came.
//
// For a statement kept on the server its SQLSTATE says so: 0A000, a plan whose
// result type changed under it, and 26000, a statement the server no longer
// has. One that is parsed every time is held to the server's word on its
// columns whenever it runs, which leaves the parameter types it is parsed and
// bound with, and where the error comes says whether they are at fault. Before
// the Bind is answered it is the server refusing the statement as parsed with
// them, or an argument as encoded for them, whatever the code: a type the
// server no longer has is XX000, a value the old type will not take is 22P02.
// After it, the error is the statement's own, raised as it ran, and drops
// nothing: a row-level security policy refusing a row is class 42 as well.
func outdated(stmt *statement, code string, bound bool) bool {
	if stmt.name != "" {
		return code == "0A000" || code == "26000"
	}
	return !bound
}

// forgetStatements drops every statement, cached or prepared by the caller,
// for after a reset that deallocated them on the server.
func (s *Conn) forgetStatements() {
	s.cache.clear()
	clear(s.prepared)
	s.closing = s.closing[:0]
}

// tag reads a CommandComplete body, reusing the previous tag's string when the
// text is the same, which saves the allocation on every repeated statement.
// The tags that end a transaction are constants and leave the remembered one
// alone, or a statement run once in each transaction would have its tag
// allocated every time, with COMMIT between any two of them.
func (s *Conn) tag(body []byte) string {
	if len(body) > 0 && body[len(body)-1] == 0 {
		body = body[:len(body)-1]
	}
	switch string(body) {
	case "COMMIT":
		return "COMMIT"
	case "ROLLBACK":
		return "ROLLBACK"
	}
	if string(body) != s.lastTag {
		s.lastTag = string(body)
	}
	return s.lastTag
}
