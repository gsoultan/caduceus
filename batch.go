// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"strconv"

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
	stmts, temporary, err := s.prepareBatch(queries)
	if err != nil {
		return nil, s.finish(err)
	}
	buf, windows, err := encodeBatch(s.conn.Buffer(), queries, stmts, temporary)
	if err != nil {
		return nil, s.finish(s.discard(temporary, err))
	}
	results := &BatchResults{session: s, stmts: stmts, temporary: temporary}
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
// its replies so far without ending the transaction as a Sync would.
func encodeBatch(buf []byte, queries []Query, stmts []*statement, temporary []string) ([]byte, []batchWindow, error) {
	var windows []batchWindow
	var err error
	first, start := 0, 0
	for i, query := range queries {
		if buf, err = appendBind(buf, stmts[i], query.Arguments); err != nil {
			return buf, nil, err
		}
		buf = wire.AppendExecute(buf, "", 0)
		if len(buf)-start < pipelineWindow || i == len(queries)-1 {
			continue
		}
		buf = wire.AppendFlush(buf)
		windows = append(windows, batchWindow{first: first, end: len(buf)})
		first, start = i+1, len(buf)
	}
	for _, name := range temporary {
		buf = wire.AppendClose(buf, 'S', name)
	}
	buf = wire.AppendSync(buf)
	if len(windows) > 0 {
		windows = append(windows, batchWindow{first: first, end: len(buf)})
	}
	return buf, windows, nil
}

// prepareBatch finds or prepares a statement for every query. Without a cache
// the statements are given names of their own for the length of the batch,
// since one unnamed statement cannot serve several queries at once, and are
// returned for closing.
func (s *Conn) prepareBatch(queries []Query) ([]*statement, []string, error) {
	stmts := make([]*statement, len(queries))
	var missing []*statement
	bySQL := map[string]*statement{}
	for i, query := range queries {
		if s.cache != nil {
			stmts[i] = s.cache.get(query.SQL)
		}
		if stmts[i] == nil {
			stmts[i] = bySQL[query.SQL]
		}
		if stmts[i] == nil {
			s.named++
			stmt := &statement{name: "caduceus_" + strconv.FormatUint(s.named, 36), sql: query.SQL}
			bySQL[query.SQL] = stmt
			missing = append(missing, stmt)
			stmts[i] = stmt
		}
	}
	if len(missing) == 0 {
		return stmts, nil, nil
	}
	if err := s.describeAll(missing); err != nil {
		// Those parsed before the one that failed exist on the server, and
		// nothing refers to them now.
		for _, stmt := range missing {
			s.closing = append(s.closing, stmt.name)
		}
		return nil, nil, err
	}

	var temporary []string
	for _, stmt := range missing {
		if s.cache == nil {
			temporary = append(temporary, stmt.name)
			continue
		}
		// An eviction is closed ahead of the next exchange's Parse, after this
		// batch, so a statement this batch is about to bind is never closed
		// under it.
		if evicted := s.cache.put(stmt); evicted != nil {
			s.closing = append(s.closing, evicted.name)
		}
	}
	return stmts, temporary, nil
}

// describeAll parses and describes statements, filling in what the server
// reports for each. They go a window at a time, each behind its own Sync and
// read before the next is sent, for the reason a batch does: the descriptions
// of thousands of statements are more than the path back can hold unread.
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
// behind one Sync, and returns how many that was.
func (s *Conn) describeWindow(stmts []*statement) (int, error) {
	buf := s.closeEvicted(s.conn.Buffer())
	sent := 0
	for sent < len(stmts) && len(buf) < pipelineWindow {
		buf = wire.AppendParse(buf, stmts[sent].name, stmts[sent].sql, nil)
		buf = wire.AppendDescribe(buf, 'S', stmts[sent].name)
		sent++
	}
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
		kind, body, err := s.conn.Receive()
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

// discard closes temporary statements after a batch failed before it was sent,
// and returns the failure.
func (s *Conn) discard(temporary []string, failure error) error {
	if len(temporary) == 0 {
		return failure
	}
	buf := s.conn.Buffer()
	for _, name := range temporary {
		buf = wire.AppendClose(buf, 'S', name)
	}
	if err := s.conn.Send(wire.AppendSync(buf)); err != nil {
		return err
	}
	if _, err := s.drain(nil); err != nil {
		return err
	}
	return failure
}
