// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "encoding/binary"

// Decoder decodes pgoutput messages: the Data of each WALRecord, in order.
//
// It remembers the relations it has been told about and the transaction it is
// in, which is what names the columns and dates the changes of later messages.
// One Decoder serves one stream and is not safe for concurrent use.
type Decoder struct {
	relations map[uint32]*Relation
	current   Transaction
}

// NewDecoder returns a Decoder that knows no relations yet.
func NewDecoder() *Decoder {
	return &Decoder{relations: make(map[uint32]*Relation)}
}

// Relation returns a relation the stream has described, by id.
func (d *Decoder) Relation(id uint32) (*Relation, bool) {
	rel, ok := d.relations[id]
	return rel, ok
}

// Transaction returns the transaction the stream is in.
func (d *Decoder) Transaction() Transaction { return d.current }

// Decode decodes one pgoutput message and reports whether it was a row change.
//
// Begin and Relation update the decoder's state and are not changes. Commit,
// Origin, Type, Truncate and the rest carry no row change and are skipped, as
// is a change to a relation the stream never described.
func (d *Decoder) Decode(record []byte) (Change, bool, error) {
	if len(record) == 0 {
		return Change{}, false, ErrMalformed
	}
	body := record[1:]
	switch record[0] {
	case msgBegin:
		tx, err := parseBegin(body)
		if err != nil {
			return Change{}, false, err
		}
		d.current = tx
		return Change{}, false, nil

	case msgRelation:
		rel, err := parseRelation(body)
		if err != nil {
			return Change{}, false, err
		}
		d.relations[rel.ID] = rel
		return Change{}, false, nil

	case msgInsert, msgUpdate, msgDelete:
		if len(body) < 4 {
			return Change{}, false, ErrMalformed
		}
		rel, ok := d.relations[binary.BigEndian.Uint32(body)]
		if !ok {
			return Change{}, false, nil
		}
		change, err := decodeChange(Op(record[0]), rel, body[4:])
		if err != nil {
			return Change{}, false, err
		}
		change.Transaction = d.current
		return change, true, nil
	}
	return Change{}, false, nil
}

// decodeChange decodes the tuples of an Insert, Update or Delete straight into
// the change's maps, with no intermediate representation.
func decodeChange(op Op, rel *Relation, body []byte) (Change, error) {
	change := Change{Op: op, Relation: rel}
	r := reader{data: body}
	switch op {
	case Insert:
		change.After = r.tupleAfter(rel, tupleNew)
	case Update:
		// Before is sent only for what the table's REPLICA IDENTITY covers, and
		// not at all under the default identity unless the key changed.
		if kind := r.peek(); kind == tupleKey || kind == tupleOld {
			change.Before = r.tupleAfter(rel, kind)
		}
		change.After = r.tupleAfter(rel, tupleNew)
	case Delete:
		kind := r.peek()
		if kind != tupleKey && kind != tupleOld {
			return Change{}, ErrMalformed
		}
		change.Before = r.tupleAfter(rel, kind)
	}
	return change, r.err
}

// tupleAfter reads the tuple that follows a marker byte, checking the marker.
func (r *reader) tupleAfter(rel *Relation, marker byte) map[string]any {
	if r.byte() != marker {
		r.err = ErrMalformed
		return nil
	}
	return r.tuple(rel)
}

// tuple decodes a tuple into a freshly allocated column map, which is handed
// to the caller outright. Recycling these maps silently cleared them under any
// consumer that kept a change past its loop iteration.
func (r *reader) tuple(rel *Relation) map[string]any {
	count := int(r.uint16())
	if r.err != nil {
		return nil
	}
	data := make(map[string]any, min(count, len(rel.Columns)))
	for i := range count {
		kind := r.byte()
		var value []byte
		if kind == tupleText || kind == tupleBinary {
			value = r.take(int(int32(r.uint32())))
		}
		if r.err != nil {
			return nil
		}
		if i >= len(rel.Columns) {
			// More columns than the relation named: what cannot be named is dropped.
			continue
		}
		name := rel.Columns[i]
		switch kind {
		case tupleNull:
			data[name] = nil
		case tupleText, tupleBinary:
			data[name] = string(value)
		case tupleUnchanged:
			// Omitted rather than nil, which would be indistinguishable from a
			// real NULL and blank the column in a consumer replaying the change.
		default:
			r.err = ErrMalformed
			return nil
		}
	}
	return data
}
