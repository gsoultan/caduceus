// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "context"

// Batch is a batch built a query at a time, each with what to do with its
// result, as pgx's Batch is. Run sends it with SendBatch and hands each
// result to its query's callback, in order:
//
//	var b caduceus.Batch
//	b.Queue("INSERT INTO t (id) VALUES ($1)", 1)
//	b.Queue("SELECT count(*) FROM t").QueryRow(func(row *caduceus.Row) error {
//		return row.Scan(&n)
//	})
//	err := b.Run(ctx, conn)
//
// It is SendBatch underneath, so it runs as one implicit transaction too.
type Batch struct {
	queued []*QueuedQuery
}

// Queue adds a statement to the batch and returns it, to give it a callback.
// One given none has its result read and dropped.
func (b *Batch) Queue(sql string, args ...any) *QueuedQuery {
	q := &QueuedQuery{SQL: sql, Arguments: args}
	b.queued = append(b.queued, q)
	return q
}

// Len is how many statements are queued.
func (b *Batch) Len() int { return len(b.queued) }

// Run sends the batch on db, a Conn or a Tx, and runs each statement's
// callback on its result in order. The first error, a statement's or a
// callback's, stops the callbacks and is returned; the rest of the batch is
// still read, so the connection is free again when Run returns.
func (b *Batch) Run(ctx context.Context, db BatchSender) error {
	queries := make([]Query, len(b.queued))
	for i, q := range b.queued {
		queries[i] = Query{SQL: q.SQL, Arguments: q.Arguments}
	}
	results, err := db.SendBatch(ctx, queries)
	if err != nil {
		return err
	}
	err = b.callBack(results)
	if closed := results.Close(); err == nil {
		err = closed
	}
	return err
}

// callBack runs each queued statement's callback on its result.
func (b *Batch) callBack(results *BatchResults) error {
	for _, q := range b.queued {
		if err := q.handle(results); err != nil {
			return err
		}
	}
	return nil
}
