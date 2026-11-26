// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "fmt"

// ParseMessage parses one pgoutput message, the Data of a WALRecord, as it is
// on the wire, protocol versions 1 to 4: what Decoder makes row changes of,
// and the rest, Commit, Origin, Type, Truncate, logical decoding messages,
// streamed and two-phase transactions, that it passes over.
//
// inStream is whether the record is between a StreamStart and a StreamStop,
// where a Relation, Type, Insert, Update, Delete, Truncate or Message carries
// the streamed transaction's Xid first. The caller keeps track, as the
// messages say: true from a *StreamStartMessage, false from a
// *StreamStopMessage.
//
// What ParseMessage returns aliases record: copy a tuple's Data to keep it
// past the record.
func ParseMessage(record []byte, inStream bool) (Message, error) {
	if len(record) == 0 {
		return nil, ErrMalformed
	}
	r := reader{data: record[1:]}
	message := r.message(record[0], inStream)
	if r.err != nil {
		return nil, r.err
	}
	if message == nil {
		return nil, fmt.Errorf("%w: unknown message type %q", ErrMalformed, record[0])
	}
	return message, nil
}

// message reads the body of a message of type kind.
func (r *reader) message(kind byte, inStream bool) Message {
	switch kind {
	case msgBegin:
		return &BeginMessage{FinalLSN: LSN(r.uint64()), CommitTime: r.timestamp(), Xid: r.uint32()}
	case msgCommit:
		return &CommitMessage{Flags: r.byte(), CommitLSN: LSN(r.uint64()), TransactionEndLSN: LSN(r.uint64()), CommitTime: r.timestamp()}
	case msgOrigin:
		return &OriginMessage{CommitLSN: LSN(r.uint64()), Name: r.string()}
	case msgStreamStart:
		return &StreamStartMessage{Xid: r.uint32(), FirstSegment: r.byte() == 1}
	case msgStreamStop:
		return &StreamStopMessage{}
	case msgStreamCommit:
		return &StreamCommitMessage{Xid: r.uint32(), Flags: r.byte(), CommitLSN: LSN(r.uint64()), TransactionEndLSN: LSN(r.uint64()), CommitTime: r.timestamp()}
	case msgStreamAbort:
		return r.streamAbort()
	}
	if message := r.prepared(kind); message != nil {
		return message
	}
	return r.change(kind, r.xid(inStream))
}

// xid reads the Xid a message inside a stream carries first.
func (r *reader) xid(inStream bool) uint32 {
	if !inStream {
		return 0
	}
	return r.uint32()
}

// change reads the messages that a stream prefixes with an Xid.
func (r *reader) change(kind byte, xid uint32) Message {
	switch kind {
	case msgRelation:
		return r.relation(xid)
	case msgType:
		return &TypeMessage{Xid: xid, DataType: r.uint32(), Namespace: r.string(), Name: r.string()}
	case msgInsert:
		m := &InsertMessage{Xid: xid, RelationID: r.uint32()}
		m.Tuple = r.markedTuple(tupleNew)
		return m
	case msgUpdate:
		m := &UpdateMessage{Xid: xid, RelationID: r.uint32()}
		if kind := r.peek(); kind == TupleKey || kind == TupleOld {
			m.OldTupleType = kind
			m.OldTuple = r.markedTuple(kind)
		}
		m.NewTuple = r.markedTuple(tupleNew)
		return m
	case msgDelete:
		m := &DeleteMessage{Xid: xid, RelationID: r.uint32(), OldTupleType: r.peek()}
		if m.OldTupleType != TupleKey && m.OldTupleType != TupleOld {
			r.err = ErrMalformed
			return nil
		}
		m.OldTuple = r.markedTuple(m.OldTupleType)
		return m
	case msgTruncate:
		return r.truncate(xid)
	case msgMessage:
		return r.logicalMessage(xid)
	}
	return nil
}

// prepared reads the two-phase commit messages of protocol version 3.
func (r *reader) prepared(kind byte) Message {
	switch kind {
	case msgBeginPrepare:
		return &BeginPrepareMessage{PrepareLSN: LSN(r.uint64()), EndPrepareLSN: LSN(r.uint64()), PrepareTime: r.timestamp(), Xid: r.uint32(), Gid: r.string()}
	case msgPrepare, msgStreamPrepare:
		return &PrepareMessage{Flags: r.byte(), PrepareLSN: LSN(r.uint64()), EndPrepareLSN: LSN(r.uint64()), PrepareTime: r.timestamp(), Xid: r.uint32(), Gid: r.string(), Streamed: kind == msgStreamPrepare}
	case msgCommitPrepared:
		return &CommitPreparedMessage{Flags: r.byte(), CommitLSN: LSN(r.uint64()), EndCommitLSN: LSN(r.uint64()), CommitTime: r.timestamp(), Xid: r.uint32(), Gid: r.string()}
	case msgRollbackPrepared:
		return &RollbackPreparedMessage{Flags: r.byte(), EndLSN: LSN(r.uint64()), RollbackLSN: LSN(r.uint64()), PrepareTime: r.timestamp(), RollbackTime: r.timestamp(), Xid: r.uint32(), Gid: r.string()}
	}
	return nil
}

// streamAbort reads a StreamAbort, with the abort's LSN and time where
// protocol version 4 sends them.
func (r *reader) streamAbort() Message {
	m := &StreamAbortMessage{Xid: r.uint32(), SubXid: r.uint32()}
	if len(r.data) >= 16 {
		m.AbortLSN, m.AbortTime = LSN(r.uint64()), r.timestamp()
	}
	return m
}

func (r *reader) relation(xid uint32) Message {
	m := &RelationMessage{Xid: xid, RelationID: r.uint32(), Namespace: r.string(), RelationName: r.string(), ReplicaIdentity: r.byte()}
	count := int(r.uint16())
	if r.err == nil && count > len(r.data) {
		r.err = ErrMalformed
		return nil
	}
	m.Columns = make([]RelationColumn, 0, count)
	for range count {
		m.Columns = append(m.Columns, RelationColumn{Flags: r.byte(), Name: r.string(), DataType: r.uint32(), TypeModifier: int32(r.uint32())})
	}
	return m
}

func (r *reader) truncate(xid uint32) Message {
	count := int(r.uint32())
	m := &TruncateMessage{Xid: xid, Option: r.byte()}
	if r.err == nil && count > len(r.data)/4 {
		r.err = ErrMalformed
		return nil
	}
	m.RelationIDs = make([]uint32, count)
	for i := range m.RelationIDs {
		m.RelationIDs[i] = r.uint32()
	}
	return m
}

func (r *reader) logicalMessage(xid uint32) Message {
	m := &LogicalDecodingMessage{Xid: xid, Transactional: r.byte() == 1, LSN: LSN(r.uint64()), Prefix: r.string()}
	m.Content = r.take(int(int32(r.uint32())))
	return m
}

// markedTuple reads a tuple behind the marker byte it must have.
func (r *reader) markedTuple(marker byte) *TupleData {
	if r.byte() != marker {
		r.err = ErrMalformed
		return nil
	}
	return r.tupleData()
}

// tupleData reads a tuple's columns, each value aliasing the record.
func (r *reader) tupleData() *TupleData {
	count := int(r.uint16())
	if r.err != nil || count > len(r.data) {
		r.err = ErrMalformed
		return nil
	}
	tuple := &TupleData{Columns: make([]TupleColumn, count)}
	for i := range tuple.Columns {
		column := &tuple.Columns[i]
		column.Kind = r.byte()
		switch column.Kind {
		case TupleNull, TupleUnchanged:
		case TupleText, TupleBinary:
			column.Data = r.take(int(int32(r.uint32())))
		default:
			r.err = ErrMalformed
			return nil
		}
	}
	return tuple
}
