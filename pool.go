// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// ErrPoolClosed is a Pool used after Close.
var ErrPoolClosed = errors.New("caduceus: the pool is closed")

// ErrPoolConfig is a PoolConfig that cannot make a pool.
var ErrPoolConfig = errors.New("caduceus: invalid pool configuration")

// idleProbeAfter is how long a connection sits idle before it is checked,
// without a round trip, for a server that hung up on it as it is handed out.
const idleProbeAfter = time.Second

// Pool is a pool of connections, safe for concurrent use, and what most
// programs hold in place of a Conn.
//
// Exec, Query, QueryRow, SendBatch, CopyFrom and Begin each take a connection
// and give it back when they are done with it: Exec and CopyFrom at once,
// Query's rows once they have been read to the end or closed, QueryRow's row
// once it is scanned, a batch once it is closed, and a transaction once it is
// committed or rolled back. Rows left open, and a transaction left unsettled,
// keep their connection.
//
// What comes back is caduceus's own: *Rows, *Row, *Tx and *BatchResults, so
// CollectRows, the RowTo functions and BeginFunc work on a Pool as they do on
// a Conn.
//
// A connection given back with a transaction open is rolled back; one in the
// middle of a result, closed, past MaxConnLifetime, or whose ResetQuery failed
// is closed rather than kept. One idle for longer than a second is checked as
// it is handed out, without a round trip, for a server that ended it.
//
// The pool opens connections as they are asked for, up to MaxConns, and keeps
// MinConns open while idle. A pooler in front of the server, such as nextar's
// gpoolproxy or PgBouncer in transaction mode, needs nothing more of it; one
// that does not track prepared statements needs
// Options.StatementCacheCapacity set to -1.
type Pool struct {
	config PoolConfig
	// slots holds one token for each connection open or being opened, so
	// there are never more than MaxConns.
	slots chan struct{}

	mu     sync.Mutex
	idle   []*pooled
	closed bool
	// done ends the health check.
	done chan struct{}
	wg   sync.WaitGroup

	stat poolCounters
}

// pooled is one of a Pool's connections, with when it was opened and when it
// was last given back.
type pooled struct {
	conn     *Conn
	born     time.Time
	returned time.Time
}

var _ TxBeginner = (*Pool)(nil)

// NewPool parses connString with ParsePoolConfig and opens a pool on it.
func NewPool(ctx context.Context, connString string) (*Pool, error) {
	config, err := ParsePoolConfig(connString)
	if err != nil {
		return nil, err
	}
	return NewPoolConfig(ctx, config)
}

// NewPoolConfig opens a pool. It opens no connection itself, as pgxpool did
// not: Ping finds a database that is not there. MinConns are opened in the
// background.
func NewPoolConfig(ctx context.Context, config *PoolConfig) (*Pool, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: nil PoolConfig", ErrPoolConfig)
	}
	c := config.Copy().withDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	p := &Pool{config: c, slots: make(chan struct{}, c.MaxConns), done: make(chan struct{})}
	p.wg.Add(1)
	go p.maintain()
	return p, nil
}

// Config returns a copy of the pool's configuration, defaults filled in, for
// a second pool built from it.
func (p *Pool) Config() *PoolConfig { return p.config.Copy() }

// Acquire takes a connection for the caller's own use, until Release. It is
// for work that needs one connection throughout: a session setting, an
// advisory lock, LISTEN.
func (p *Pool) Acquire(ctx context.Context) (*PoolConn, error) {
	m, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	return &PoolConn{Conn: m.conn, pool: p, member: m}, nil
}

// Exec runs a statement on a connection of the pool; see Conn.Exec.
func (p *Pool) Exec(ctx context.Context, sql string, args ...any) (CommandTag, error) {
	m, err := p.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer p.release(m)
	return m.conn.Exec(ctx, sql, args...)
}

// Query runs a query on a connection of the pool, which goes back once the
// rows are read to the end or closed; see Conn.Query.
func (p *Pool) Query(ctx context.Context, sql string, args ...any) (*Rows, error) {
	m, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := m.conn.Query(ctx, sql, args...)
	if err != nil {
		p.release(m)
		return nil, err
	}
	rows.release = p.once(m)
	return rows, nil
}

// QueryRow runs a query expected to return at most one row, on a connection
// of the pool that goes back once the row is scanned or closed.
func (p *Pool) QueryRow(ctx context.Context, sql string, args ...any) *Row {
	m, err := p.acquire(ctx)
	if err != nil {
		return &Row{err: err}
	}
	row := m.conn.QueryRow(ctx, sql, args...)
	if row.err != nil {
		p.release(m)
		return row
	}
	row.rows.release = p.once(m)
	return row
}

// SendBatch sends a batch on a connection of the pool, which goes back when
// the results are closed; see Conn.SendBatch.
func (p *Pool) SendBatch(ctx context.Context, queries []Query) (*BatchResults, error) {
	m, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	results, err := m.conn.SendBatch(ctx, queries)
	if err != nil {
		p.release(m)
		return nil, err
	}
	results.release = p.once(m)
	return results, nil
}

// CopyFrom copies rows into a table on a connection of the pool; see
// Conn.CopyFrom.
func (p *Pool) CopyFrom(ctx context.Context, table, columns []string, rows CopySource) (int64, error) {
	m, err := p.acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer p.release(m)
	return m.conn.CopyFrom(ctx, table, columns, rows)
}

// CopyTo streams a COPY ... TO STDOUT into w; see Conn.CopyTo.
func (p *Pool) CopyTo(ctx context.Context, w io.Writer, sql string) (CommandTag, error) {
	m, err := p.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer p.release(m)
	return m.conn.CopyTo(ctx, w, sql)
}

// Begin starts a transaction on a connection of the pool, which stays the
// transaction's until Commit or Rollback.
func (p *Pool) Begin(ctx context.Context) (*Tx, error) {
	return p.BeginTx(ctx, TxOptions{})
}

// BeginTx is Begin with the transaction's characteristics.
func (p *Pool) BeginTx(ctx context.Context, options TxOptions) (*Tx, error) {
	m, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := m.conn.BeginTx(ctx, options)
	if err != nil {
		p.release(m)
		return nil, err
	}
	tx.release = p.once(m)
	return tx, nil
}

// Ping makes a round trip to the server on a connection of the pool.
func (p *Pool) Ping(ctx context.Context) error {
	m, err := p.acquire(ctx)
	if err != nil {
		return err
	}
	defer p.release(m)
	return m.conn.Ping(ctx)
}

// Close closes the idle connections, and each one in use as it is given back,
// and stops the health check. Using the pool afterwards returns
// ErrPoolClosed. It is idempotent.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	idle := p.idle
	p.idle, p.closed = nil, true
	close(p.done)
	p.mu.Unlock()
	for _, m := range idle {
		p.destroy(m)
	}
	p.wg.Wait()
}

// acquire takes a slot and a connection: an idle one that is still sound, or
// a new one.
func (p *Pool) acquire(ctx context.Context) (*pooled, error) {
	start := time.Now()
	if err := p.slot(ctx); err != nil {
		if !errors.Is(err, ErrPoolClosed) {
			p.stat.canceled.Add(1)
		}
		return nil, err
	}
	if m, ok := p.takeIdle(time.Now()); ok {
		p.stat.acquired(start)
		return m, nil
	}
	m, err := p.connect(ctx)
	if err != nil {
		<-p.slots
		return nil, err
	}
	p.stat.acquired(start)
	return m, nil
}

// takeIdle takes the most recently returned idle connection that is still
// sound, closing those that are not.
func (p *Pool) takeIdle(now time.Time) (*pooled, bool) {
	for {
		p.mu.Lock()
		n := len(p.idle)
		if p.closed || n == 0 {
			p.mu.Unlock()
			return nil, false
		}
		m := p.idle[n-1]
		p.idle[n-1] = nil
		p.idle = p.idle[:n-1]
		p.mu.Unlock()
		// CheckConn reads the socket without a round trip; a connection back
		// less than idleProbeAfter ago is taken on trust, which keeps a busy
		// pool's hot path free of it.
		if p.stale(m, now) || (now.Sub(m.returned) >= idleProbeAfter && m.conn.CheckConn() != nil) {
			p.destroy(m)
			continue
		}
		p.stat.inUse.Add(1)
		return m, true
	}
}

// slot waits for room for one more connection in use: until ctx is done or
// the pool closes. A connection is opened only by a caller holding a slot
// and finding none idle, and is put back idle before its slot is freed, so
// no more than MaxConns are ever open.
func (p *Pool) slot(ctx context.Context) error {
	select {
	case <-p.done:
		return ErrPoolClosed
	case p.slots <- struct{}{}:
		return nil
	default:
	}
	p.stat.empty.Add(1)
	select {
	case p.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("caduceus: waiting for a pool connection: %w", context.Cause(ctx))
	case <-p.done:
		return ErrPoolClosed
	}
}

// connect opens a connection, running AfterConnect on it. The caller holds a
// slot for it.
func (p *Pool) connect(ctx context.Context) (*pooled, error) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return nil, ErrPoolClosed
	}
	p.stat.constructing.Add(1)
	defer p.stat.constructing.Add(-1)
	conn, err := ConnectConfig(ctx, p.config.ConnConfig.Copy(), p.config.Options)
	if err != nil {
		return nil, err
	}
	if p.config.AfterConnect != nil {
		if err := p.config.AfterConnect(ctx, conn); err != nil {
			closeConn(conn)
			return nil, fmt.Errorf("caduceus: AfterConnect: %w", err)
		}
	}
	p.stat.total.Add(1)
	p.stat.newConns.Add(1)
	p.stat.inUse.Add(1)
	now := time.Now()
	return &pooled{conn: conn, born: now, returned: now}, nil
}

// release gives m back, kept if it is sound and idle and closed otherwise,
// and then frees its slot.
func (p *Pool) release(m *pooled) {
	defer func() { <-p.slots }()
	p.stat.inUse.Add(-1)
	if !p.recyclable(m) {
		p.destroy(m)
		return
	}
	m.returned = time.Now()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		p.destroy(m)
		return
	}
	p.idle = append(p.idle, m)
	p.mu.Unlock()
}

// recyclable reports whether m can be handed to the next caller, rolling
// back what the last one left open and running ResetQuery.
func (p *Pool) recyclable(m *pooled) bool {
	c := m.conn
	if c.IsClosed() || c.Busy() || (p.config.MaxConnLifetime > 0 && time.Since(m.born) >= p.config.MaxConnLifetime) {
		return false
	}
	if c.TxStatus() == 'I' && p.config.ResetQuery == "" {
		// The common release: nothing to undo, so no context is built, and
		// Unwind only spends the Tx values begun on the connection.
		return c.Unwind(context.Background()) == nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.config.ResetTimeout)
	defer cancel()
	if err := c.Unwind(ctx); err != nil {
		return false
	}
	if p.config.ResetQuery != "" {
		if _, err := c.Exec(ctx, p.config.ResetQuery); err != nil {
			return false
		}
	}
	return !c.IsClosed()
}

// destroy closes m.
func (p *Pool) destroy(m *pooled) {
	closeConn(m.conn)
	p.stat.total.Add(-1)
}

func closeConn(c *Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = c.Close(ctx)
}

// stale reports whether m has outlived the pool's limits at now.
func (p *Pool) stale(m *pooled, now time.Time) bool {
	c := p.config
	if c.MaxConnLifetime > 0 && now.Sub(m.born) >= c.MaxConnLifetime {
		p.stat.lifetimeDestroyed.Add(1)
		return true
	}
	if c.MaxConnIdleTime > 0 && now.Sub(m.returned) >= c.MaxConnIdleTime {
		p.stat.idleDestroyed.Add(1)
		return true
	}
	return false
}

// once makes a release safe to call more than once.
func (p *Pool) once(m *pooled) func() {
	var done atomic.Bool
	return func() {
		if done.CompareAndSwap(false, true) {
			p.release(m)
		}
	}
}

// maintain closes stale idle connections and keeps MinConns open, every
// HealthCheckPeriod, until the pool closes.
func (p *Pool) maintain() {
	defer p.wg.Done()
	p.topUp()
	if p.config.HealthCheckPeriod <= 0 {
		return
	}
	t := time.NewTicker(p.config.HealthCheckPeriod)
	defer t.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-t.C:
			p.reap()
			p.topUp()
		}
	}
}

// reap closes the idle connections past their lifetime or idle time.
func (p *Pool) reap() {
	now := time.Now()
	p.mu.Lock()
	var stale []*pooled
	kept := p.idle[:0]
	for _, m := range p.idle {
		if p.stale(m, now) {
			stale = append(stale, m)
		} else {
			kept = append(kept, m)
		}
	}
	clear(p.idle[len(kept):])
	p.idle = kept
	p.mu.Unlock()
	for _, m := range stale {
		p.destroy(m)
	}
}

// topUp opens connections until MinConns are open, without waiting for a
// slot: a pool in full use has enough. Each is opened holding a slot, and
// release frees it.
func (p *Pool) topUp() {
	for p.stat.total.Load()+p.stat.constructing.Load() < int64(p.config.MinConns) {
		select {
		case p.slots <- struct{}{}:
		default:
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			select {
			case <-p.done:
				cancel()
			case <-ctx.Done():
			}
		}()
		m, err := p.connect(ctx)
		cancel()
		if err != nil {
			<-p.slots
			return
		}
		p.release(m)
		if p.idleCount() == 0 {
			// Taken at once by a caller, or not kept: nothing to top up with.
			return
		}
	}
}

func (p *Pool) idleCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle)
}

// PoolConn is a connection taken from a Pool with Acquire. It is a Conn in
// every way, until Release gives it back; it must not be used after.
type PoolConn struct {
	*Conn
	pool     *Pool
	member   *pooled
	released atomic.Bool
}

// Release gives the connection back to its pool. It is idempotent.
func (c *PoolConn) Release() {
	if c.released.CompareAndSwap(false, true) {
		c.pool.release(c.member)
	}
}

// Hijack takes the connection out of the pool for good: Release then does
// nothing, and closing it is the caller's.
func (c *PoolConn) Hijack() *Conn {
	if !c.released.CompareAndSwap(false, true) {
		return nil
	}
	c.pool.stat.inUse.Add(-1)
	c.pool.stat.total.Add(-1)
	<-c.pool.slots
	return c.Conn
}
