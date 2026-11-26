// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"

	"github.com/gsoultan/caduceus/wire"
)

// pipelineWindow is how much a pipeline or a batch sends before it waits for
// the reader to catch up. At most two windows are unread by the server at
// once, 64 KiB, which is the receive window a server offers before it has read
// anything, so they are taken off the client's hands even by a server that has
// stopped reading because the client has not read its replies. Anything
// larger and the two could each be waiting for the other to read.
//
// A window closes once it has passed the size, so it is the size and one
// statement more. A statement larger than a window by itself, a long text or a
// large argument, goes in a window of its own, sent only once every statement
// before it has been read: the server answers nothing of a statement until it
// has read the whole of it, so with nothing else owed to the client it is
// always reading. Sent behind the statements before it, megabytes of it could
// be waiting on a server that was waiting on the client to read their results.
const pipelineWindow = 32 << 10

// oversized reports whether an encoded statement of n bytes is too large to
// share a window.
func oversized(n int) bool { return n > pipelineWindow }

// PipelineResults reads a pipeline's results, one statement at a time, in
// the order they were queued.
//
// Unlike a batch, every statement of a pipeline is its own implicit
// transaction: one that fails reports its error and the rest still run.
// Statements are sent a window at a time as their results are read, so a
// pipeline of a million statements never holds more than two windows of
// encoded messages.
//
// Inside a transaction there is nothing to isolate, since the first failure
// aborts the transaction whatever follows it. There a window shares one Sync,
// which lets the server answer a whole window in a few packets rather than
// one per statement: after a failure the rest of its window is skipped by the
// server and reports the same error, and later windows fail with 25P02.
//
// On a connection that keeps no statements on the server, a statement is
// parsed in every Sync that runs it. Outside a transaction that is once for
// every statement of the pipeline, which is what its statements each being a
// transaction of their own costs there; inside one it is once for a run of
// the same statement in a window.
type PipelineResults struct {
	session *Conn
	queries []Query
	stmts   []*statement

	// grouped is a pipeline inside a transaction block, where a window of
	// statements shares one Sync. ends holds, for each window sent and not
	// yet read to its end, the index just past its last statement, which is
	// where its ReadyForQuery comes.
	grouped bool
	ends    []int
	// skipUntil is the end of a window the server stopped running after
	// skipErr: the statements before it have no replies.
	skipUntil int
	skipErr   error
	// live is whether the current statement has replies on the connection.
	live bool

	// sent is how many statements have been sent. failed holds, for a
	// statement that could not be encoded, why. Outside a transaction it is
	// sent as a bare Sync, so that every statement still has exactly one
	// ReadyForQuery; inside one it is not sent at all.
	sent   int
	failed map[int]error
	// windowStart is the first statement of the last window sent; reading it
	// is the signal to send the next. held is the statement after that window
	// held back from it, too large to share one.
	windowStart int
	held        bool

	next    int
	current *Rows
	failure error
	settled bool
	closed  bool
}

// SendPipeline sends queries without waiting for each reply and returns their
// results in order. Every query is its own implicit transaction, so a failure
// affects only that query; wrap the pipeline in Begin and Commit for one
// transaction around all of them. Close the results before the next call on
// the connection; Close also runs every query not yet read.
func (s *Conn) SendPipeline(ctx context.Context, queries []Query) (*PipelineResults, error) {
	if len(queries) == 0 {
		return nil, ErrEmptyBatch
	}
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	stmts, err := s.prepareBatch(queries)
	if err != nil {
		return nil, s.finish(err)
	}
	// A transaction's BEGIN went with the describe, if there was one. One
	// still waiting goes alone and is answered first: only the first window
	// could carry it, and the windows behind it are sent before that window
	// is answered, so behind a BEGIN the server refused they would run, and
	// commit, on their own.
	if err := s.flushBegin(); err != nil {
		return nil, s.finish(err)
	}
	p := &PipelineResults{session: s, queries: queries, stmts: stmts}
	p.grouped = s.conn.TxStatus() != wire.TxIdle
	if err := p.sendWindow(); err != nil {
		return nil, s.finish(err)
	}
	return p, nil
}

// sendWindow encodes and sends the next window of statements.
func (p *PipelineResults) sendWindow() error {
	p.windowStart, p.held = p.sent, false
	buf := p.session.conn.Buffer()
	// parsed is the statement the unnamed statement holds since the last Sync,
	// for a connection that keeps none on the server: nothing at the start of
	// a window, and nothing again after every Sync.
	var parsed *statement
	for p.sent < len(p.queries) && len(buf) < pipelineWindow && !p.held {
		buf, parsed = p.encodeNext(buf, parsed)
	}
	if p.grouped {
		buf = wire.AppendSync(buf)
		p.ends = append(p.ends, p.sent)
	}
	return p.session.conn.Send(buf)
}

// encodeNext appends the next statement to the window in buf, parsed unless it
// is the one parsed last in its Sync, and returns the window and the statement
// parsed last now. One too large to share a window is held back for a window
// of its own, and not counted sent.
func (p *PipelineResults) encodeNext(buf []byte, parsed *statement) ([]byte, *statement) {
	stmt := statementAt(p.stmts, p.sent)
	encoded := buf
	if stmt != parsed {
		encoded = appendParsed(encoded, stmt)
	}
	encoded, err := p.session.appendBound(encoded, stmt, p.queries[p.sent].Arguments)
	switch {
	case err != nil:
		// What was encoded of it is left behind buf, to be written over.
		p.recordFailure(p.sent, err)
	case oversized(len(encoded)-len(buf)) && p.sent > p.windowStart:
		p.held = true
		return buf, parsed
	default:
		buf, parsed = wire.AppendExecute(encoded, "", 0), stmt
	}
	if !p.grouped {
		buf, parsed = wire.AppendSync(buf), nil
	}
	p.sent++
	return buf, parsed
}

// due reports whether the next window is to be sent before the statement at
// next is read: once the reader has reached the last window sent, which keeps
// the server supplied and never has more than two windows unread by it, and
// for a statement held back, too large to share a window, once every
// statement before it has been read.
func (p *PipelineResults) due() bool {
	switch {
	case p.sent >= len(p.queries):
		return false
	case p.held:
		return p.next >= p.sent
	}
	return p.next >= p.windowStart
}

func (p *PipelineResults) recordFailure(i int, err error) {
	if p.failed == nil {
		p.failed = make(map[int]error)
	}
	p.failed[i] = err
}

// statementInto positions rows at the next statement's result.
func (p *PipelineResults) statementInto(rows *Rows) error {
	p.finishCurrent()
	if p.settled {
		if p.failure != nil {
			return p.failure
		}
		return errBatchExhausted
	}
	if p.next >= len(p.queries) {
		return errBatchExhausted
	}
	if p.due() {
		if err := p.sendWindow(); err != nil {
			p.settled = true
			return p.remember(p.session.finish(err))
		}
	}
	*rows = Rows{}
	rows.init(p.session, statementAt(p.stmts, p.next), true)
	rows.ownSync = !p.grouped
	p.live = true
	if err, ok := p.failed[p.next]; ok {
		rows.err = err
		// Inside a transaction a statement that could not be encoded was
		// never sent, so it has nothing to read.
		p.live = !p.grouped
	} else if p.next < p.skipUntil {
		rows.err, p.live = p.skipErr, false
	}
	rows.ended = !p.live
	p.current = rows
	p.next++
	return nil
}

func (p *PipelineResults) finishCurrent() {
	if p.current == nil {
		return
	}
	current := p.current
	p.current = nil
	current.Close()
	// The server described the statement differently from how it was
	// remembered, which the statements that share its Parse cannot see.
	restate(p.stmts, statementAt(p.stmts, p.next-1), current.stmt)
	if current.err != nil && p.failure == nil {
		p.failure = current.err
	}
	if current.settled {
		p.settled = true
	}
	if !p.grouped || p.settled || len(p.ends) == 0 {
		return
	}
	if p.live && current.err != nil {
		// The server skips the rest of the window, up to its Sync.
		p.skipUntil, p.skipErr = p.ends[0], current.err
		p.endWindow()
		return
	}
	if p.next == p.ends[0] {
		p.endWindow()
	}
}

// endWindow reads up to the ReadyForQuery that ends the oldest window.
func (p *PipelineResults) endWindow() {
	p.ends = p.ends[1:]
	if _, err := p.session.drain(nil); err != nil && !isServerError(err) {
		p.settled = true
		p.remember(p.session.finish(err))
	}
}

func (p *PipelineResults) remember(err error) error {
	if err != nil && p.failure == nil {
		p.failure = err
	}
	return p.failure
}

// Exec reads the next statement's result, discarding any rows, and returns
// its command tag.
func (p *PipelineResults) Exec() (CommandTag, error) {
	rows := new(Rows)
	if err := p.statementInto(rows); err != nil {
		return "", err
	}
	rows.Close()
	return CommandTag(rows.tag), rows.err
}

// Query returns the next statement's rows. Reading the next result, or
// closing the pipeline, closes them.
func (p *PipelineResults) Query() (*Rows, error) {
	rows := new(Rows)
	if err := p.statementInto(rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// QueryRow returns the next statement's result as a single row.
func (p *PipelineResults) QueryRow() *Row {
	row := new(Row)
	row.err = p.statementInto(&row.rows)
	return row
}

// Close runs and reads every statement not yet read, and returns the first
// error of the whole pipeline. It is idempotent, and the connection is free
// once it returns.
func (p *PipelineResults) Close() error {
	if p.closed {
		return p.failure
	}
	p.closed = true
	var rows Rows
	for !p.settled && p.next < len(p.queries) {
		if p.statementInto(&rows) != nil {
			break
		}
	}
	p.finishCurrent()
	if p.settled {
		return p.failure
	}
	p.settled = true
	return p.remember(p.session.finish(nil))
}
