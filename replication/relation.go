// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// Relation is what a Relation message says about a table: enough to name the
// columns of every later row change for it.
type Relation struct {
	ID        uint32
	Namespace string
	Name      string
	Columns   []string
}

// parseRelation reads a Relation message after its type byte.
func parseRelation(body []byte) (*Relation, error) {
	r := reader{data: body}
	rel := &Relation{ID: r.uint32(), Namespace: r.string(), Name: r.string()}
	r.byte() // replica identity setting
	count := int(r.uint16())
	if r.err == nil && count > len(r.data) {
		return nil, ErrMalformed
	}
	rel.Columns = make([]string, 0, count)
	for range count {
		r.byte() // flags: part of the key
		rel.Columns = append(rel.Columns, r.string())
		r.uint32() // type OID
		r.uint32() // type modifier
	}
	if r.err != nil {
		return nil, r.err
	}
	return rel, nil
}
