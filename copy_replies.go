// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "sync/atomic"

// copyReplies reads the replies to a COPY FROM STDIN on a goroutine of its
// own while the caller writes the data.
//
// A COPY that only writes can stall for good. A server that answers rows with
// NOTICEs, a trigger that raises one per row, writes as much as it reads; once
// its socket is full of replies nobody reads it stops reading to wait, and
// the client, still writing, waits for it. Reading while writing keeps both
// moving. Notices are handed to Config.OnNotice from this goroutine.
//
// The connection is shared for as long as the goroutine runs, and only in
// this way: the goroutine reads and the caller writes, which a net.Conn and a
// tls.Conn both allow. wait joins it before the connection is used again.
//
// owed is how many ReadyForQuery messages the server owes for what the caller
// has sent or will send. The goroutine reads one only while it knows one is
// owed, so it is never left waiting for a reply that will not come; final
// says owed will not grow.
type copyReplies struct {
	session *Conn
	owed    atomic.Int64
	final   atomic.Bool
	// failed is set once the server has refused the COPY or the connection
	// has failed: the rows still to send would be discarded.
	failed atomic.Bool
	wake   chan struct{}
	done   chan struct{}

	// Written by the goroutine, read after done is closed.
	total   int64
	tag     string
	failure error
	broken  error
}

// readCopyReplies starts reading the replies to owed statements.
func (s *Conn) readCopyReplies(owed int) *copyReplies {
	r := &copyReplies{session: s, wake: make(chan struct{}, 1), done: make(chan struct{})}
	r.owed.Store(int64(owed))
	go r.run()
	return r
}

// expect raises the number of replies owed.
func (r *copyReplies) expect(owed int) {
	r.owed.Store(int64(owed))
	r.signal()
}

// wait says owed is final, waits for every reply, and returns the rows the
// COPY statements took, their last command tag, and the first error the
// server sent. broken is a failure of the connection, after which it is gone.
func (r *copyReplies) wait(owed int) (total int64, tag string, failure, broken error) {
	r.owed.Store(int64(owed))
	r.final.Store(true)
	r.signal()
	<-r.done
	return r.total, r.tag, r.failure, r.broken
}

func (r *copyReplies) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *copyReplies) run() {
	defer close(r.done)
	for read := int64(0); r.await(read); read++ {
		tag, err := r.session.drain(nil)
		if err != nil && !isServerError(err) {
			r.broken = err
			r.failed.Store(true)
			return
		}
		if err != nil && r.failure == nil {
			r.failure = err
			r.failed.Store(true)
		}
		r.tag = tag
		r.total += CommandTag(tag).RowsAffected()
	}
}

// await reports whether a reply beyond the read ones is owed, waiting until
// it is known.
func (r *copyReplies) await(read int64) bool {
	for {
		if r.owed.Load() > read {
			return true
		}
		if r.final.Load() {
			return r.owed.Load() > read
		}
		<-r.wake
	}
}
