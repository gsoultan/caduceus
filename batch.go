// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"

	"github.com/gsoultan/caduceus/wire"
)

// batch pipelines queries in at most two round trips: one to parse and
// describe the statements the cache does not know yet, if there are any, and
// one for every Bind and Execute behind a single Sync. The exchange is left
// open for the returned reader.
//
// A single Sync makes the batch one implicit transaction:
// a statement that fails makes the server skip the rest.
//
// The whole batch is encoded before any of it is sent, so an argument that
// cannot be encoded fails the call with nothing sent. A batch that fits one
// window goes in one write. A larger one goes a window at a time as its
// results are read: the server answers while it reads, and a client that
// wrote megabytes without reading any of the answers would fill the path back,
// stop the server, and then wait on it for ever.
func (s *Conn) batch(ctx context.Context, queries []Query) (*BatchResults, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	stmts, err := s.prepareBatch(queries)
	if err != nil {
		return nil, s.finish(err)
	}
	// A transaction's BEGIN still waiting, because every statement was known
	// already, goes in front of the batch: one Sync ends them both, so a BEGIN
	// the server refuses takes the whole batch with it.
	buf, windows, err := s.encodeBatch(s.appendBegin(s.conn.Buffer()), queries, stmts)
	if err != nil {
		return nil, s.finish(err)
	}
	s.began()
	results := &BatchResults{session: s, stmts: stmts, count: len(queries)}
	if len(windows) == 0 {
		err = s.conn.Send(buf)
	} else {
		// The buffer is written in parts from here on, so the connection must
		// not lend it out again in between.
		s.conn.Keep()
		results.pending, results.windows = buf, windows
		err = results.sendWindow()
	}
	if err != nil {
		return nil, s.finish(err)
	}
	return results, nil
}

// encodeBatch appends every Bind and Execute of a batch and the Sync that ends
// it. For a batch larger than one window it also returns the windows it is to
// be sent in. Each but the last ends with a Flush, which makes the server send
// its replies so far without ending the transaction as a Sync would. A
// statement too large to share a window ends the one in front of it and
// begins one of its own.
//
// A statement that is not kept on the server is parsed into the unnamed
// statement in front of its Bind, once for a run of the same statement, since
// the unnamed statement lasts until the next Parse and the batch is one Sync.
func (s *Conn) encodeBatch(buf []byte, queries []Query, stmts []*statement) ([]byte, []batchWindow, error) {
	var windows []batchWindow
	var err error
	var parsed *statement
	window := batchWindow{}
	start := 0
	for i, query := range queries {
		stmt := statementAt(stmts, i)
		mark := len(buf)
		if stmt != parsed {
			buf, parsed = appendParsed(buf, stmt), stmt
		}
		if buf, err = s.appendBound(buf, stmt, query.Arguments); err != nil {
			return buf, nil, err
		}
		buf = wire.AppendExecute(buf, "", 0)
		if oversized(len(buf)-mark) && i > window.first {
			buf = flushBefore(buf, mark)
			window.end = mark + flushSize
			windows = append(windows, window)
			window, start = batchWindow{first: i}, window.end
		}
		window.alone = window.alone || (i == window.first && oversized(len(buf)-start))
		if len(buf)-start < pipelineWindow || i == len(queries)-1 {
			continue
		}
		buf = wire.AppendFlush(buf)
		window.end = len(buf)
		windows = append(windows, window)
		window, start = batchWindow{first: i + 1}, len(buf)
	}
	buf = wire.AppendSync(buf)
	if len(windows) > 0 {
		window.end = len(buf)
		windows = append(windows, window)
	}
	return buf, windows, nil
}

// flushSize is the length of a Flush message.
const flushSize = 5

// flushBefore puts a Flush at mark in buf, ahead of what follows it.
func flushBefore(buf []byte, mark int) []byte {
	var flush [flushSize]byte
	wire.AppendFlush(flush[:0])
	buf = append(buf, flush[:]...)
	copy(buf[mark+flushSize:], buf[mark:])
	copy(buf[mark:], flush[:])
	return buf
}

// prepareBatch finds a statement for every query, asking the server about
// those the cache does not know, which also parses there the ones that are
// kept there.
func (s *Conn) prepareBatch(queries []Query) ([]*statement, error) {
	if sameSQL(queries) {
		queries = queries[:1]
	}
	stmts := make([]*statement, len(queries))
	var missing []*statement
	bySQL := map[string]*statement{}
	for i, query := range queries {
		stmts[i] = s.cache.get(query.SQL)
		if stmts[i] == nil {
			stmts[i] = bySQL[query.SQL]
		}
		if stmts[i] == nil {
			stmt := &statement{name: s.nextName(), sql: query.SQL}
			bySQL[query.SQL] = stmt
			missing = append(missing, stmt)
			stmts[i] = stmt
		}
	}
	if len(missing) == 0 {
		return stmts, nil
	}
	if err := s.describeAll(missing); err != nil {
		// Those parsed before the one that failed exist on the server, if they
		// are kept there, and nothing refers to them now.
		for _, stmt := range missing {
			if stmt.name != "" {
				s.closing = append(s.closing, stmt.name)
			}
		}
		return nil, err
	}
	// An eviction is closed ahead of the next exchange's Parse, after this
	// batch, so a statement this batch is about to bind is never closed under
	// it.
	for _, stmt := range missing {
		s.remember(stmt)
	}
	return stmts, nil
}

// describeAll parses and describes statements, filling in what the server
// reports for each. They go a window at a time, each behind its own Sync and
// read before the next is sent, for the reason a batch does: the descriptions
// of thousands of statements are more than the path back can hold unread.
// Statements that are not kept on the server each take the unnamed statement
// from the one before, which by then has been described.
func (s *Conn) describeAll(stmts []*statement) error {
	for len(stmts) > 0 {
		described, err := s.describeWindow(stmts)
		if err != nil {
			return err
		}
		stmts = stmts[described:]
	}
	return nil
}

// describeWindow parses and describes as many of stmts as fill one window,
// behind one Sync, and returns how many that was. A transaction's BEGIN still
// waiting goes in front of the first window, whose answer is read before
// anything else is sent. A statement whose text is larger than a window goes
// in one of its own, for the reason a batch sends such a statement alone.
func (s *Conn) describeWindow(stmts []*statement) (int, error) {
	buf := s.appendBegin(s.closeEvicted(s.conn.Buffer()))
	sent := 0
	for sent < len(stmts) && len(buf) < pipelineWindow && (sent == 0 || !oversized(len(stmts[sent].sql))) {
		buf = wire.AppendParse(buf, stmts[sent].name, stmts[sent].sql, nil)
		buf = wire.AppendDescribe(buf, 'S', stmts[sent].name)
		sent++
	}
	s.began()
	if err := s.conn.Send(wire.AppendSync(buf)); err != nil {
		return 0, err
	}
	return sent, s.readDescriptions(stmts[:sent])
}

// readDescriptions reads the replies to a window of Parse and Describe through
// to its ReadyForQuery.
func (s *Conn) readDescriptions(stmts []*statement) error {
	var failure error
	next := 0
	var params []uint32
	for {
		kind, body, err := s.receive()
		if err != nil {
			return err
		}
		switch kind {
		case wire.MsgParameterDescription:
			params, err = wire.ParseParameterDescription(nil, body)
		case wire.MsgRowDescription, wire.MsgNoData:
			var fields []wire.Field
			if kind == wire.MsgRowDescription {
				fields, err = wire.ParseRowDescription(nil, body)
			}
			if next < len(stmts) {
				*stmts[next] = *newStatement(stmts[next].name, stmts[next].sql, params, fields)
				next++
			}
		case wire.MsgErrorResponse:
			if failure == nil {
				failure = wire.ParseError(body)
			}
		case wire.MsgReadyForQuery:
			return failure
		}
		if err != nil {
			return err
		}
	}
}

// sameSQL reports whether every query has the same text, as the rows of a bulk
// insert do. Their statement is then held once and not once for each: 8 MiB
// less for a million of them.
func sameSQL(queries []Query) bool {
	for i := range queries {
		if queries[i].SQL != queries[0].SQL {
			return false
		}
	}
	return true
}

// statementAt returns the statement of query i, from a list that holds one for
// each query or one for all of them.
func statementAt(stmts []*statement, i int) *statement {
	if len(stmts) == 1 {
		return stmts[0]
	}
	return stmts[i]
}
