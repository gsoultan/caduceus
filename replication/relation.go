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
	m, _ := r.relation(0).(*RelationMessage)
	if r.err != nil {
		return nil, r.err
	}
	rel := &Relation{ID: m.RelationID, Namespace: m.Namespace, Name: m.RelationName, Columns: make([]string, len(m.Columns))}
	for i, column := range m.Columns {
		rel.Columns[i] = column.Name
	}
	return rel, nil
}
