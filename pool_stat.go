// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"sync/atomic"
	"time"
)

// poolCounters are a Pool's running counts, read by Stat.
type poolCounters struct {
	total        atomic.Int64
	inUse        atomic.Int64
	constructing atomic.Int64

	acquires          atomic.Int64
	acquireNanos      atomic.Int64
	empty             atomic.Int64
	canceled          atomic.Int64
	newConns          atomic.Int64
	lifetimeDestroyed atomic.Int64
	idleDestroyed     atomic.Int64
}

// acquired counts an acquire that began at start.
func (c *poolCounters) acquired(start time.Time) {
	c.acquires.Add(1)
	c.acquireNanos.Add(int64(time.Since(start)))
}

// PoolStat is a snapshot of a Pool, with the methods pgxpool's Stat had.
type PoolStat struct {
	maxConns          int32
	totalConns        int32
	acquiredConns     int32
	constructingConns int32
	acquireCount      int64
	acquireDuration   time.Duration
	emptyAcquireCount int64
	canceledAcquires  int64
	newConnsCount     int64
	lifetimeDestroyed int64
	idleDestroyed     int64
}

// Stat returns a snapshot of the pool's connections and counters.
func (p *Pool) Stat() *PoolStat {
	c := &p.stat
	total := c.total.Load()
	inUse := c.inUse.Load()
	return &PoolStat{
		maxConns:          p.config.MaxConns,
		totalConns:        int32(total),
		acquiredConns:     int32(inUse),
		constructingConns: int32(c.constructing.Load()),
		acquireCount:      c.acquires.Load(),
		acquireDuration:   time.Duration(c.acquireNanos.Load()),
		emptyAcquireCount: c.empty.Load(),
		canceledAcquires:  c.canceled.Load(),
		newConnsCount:     c.newConns.Load(),
		lifetimeDestroyed: c.lifetimeDestroyed.Load(),
		idleDestroyed:     c.idleDestroyed.Load(),
	}
}

// MaxConns is the most connections the pool opens at once.
func (s *PoolStat) MaxConns() int32 { return s.maxConns }

// TotalConns is the connections open, in use or idle.
func (s *PoolStat) TotalConns() int32 { return s.totalConns }

// AcquiredConns is the connections in use.
func (s *PoolStat) AcquiredConns() int32 { return s.acquiredConns }

// IdleConns is the connections open and not in use.
func (s *PoolStat) IdleConns() int32 { return max(0, s.totalConns-s.acquiredConns) }

// ConstructingConns is the connections being opened.
func (s *PoolStat) ConstructingConns() int32 { return s.constructingConns }

// AcquireCount is how many connections have been taken from the pool.
func (s *PoolStat) AcquireCount() int64 { return s.acquireCount }

// AcquireDuration is the time all of them took to take.
func (s *PoolStat) AcquireDuration() time.Duration { return s.acquireDuration }

// EmptyAcquireCount is how many takes had to wait for a connection to be
// given back, every one being in use.
func (s *PoolStat) EmptyAcquireCount() int64 { return s.emptyAcquireCount }

// CanceledAcquireCount is how many takes ended with their context.
func (s *PoolStat) CanceledAcquireCount() int64 { return s.canceledAcquires }

// NewConnsCount is how many connections the pool has opened.
func (s *PoolStat) NewConnsCount() int64 { return s.newConnsCount }

// MaxLifetimeDestroyCount is how many were closed for MaxConnLifetime.
func (s *PoolStat) MaxLifetimeDestroyCount() int64 { return s.lifetimeDestroyed }

// MaxIdleDestroyCount is how many were closed for MaxConnIdleTime.
func (s *PoolStat) MaxIdleDestroyCount() int64 { return s.idleDestroyed }
