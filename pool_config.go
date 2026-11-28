// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// Defaults a PoolConfig falls back to for a field left at zero. They are
// nextar's (github.com/gsoultan/nextar), so a pool moved between the two keeps
// its behaviour.
const (
	DefaultPoolMaxConnLifetime   = time.Hour
	DefaultPoolMaxConnIdleTime   = 30 * time.Minute
	DefaultPoolHealthCheckPeriod = time.Minute
	DefaultPoolResetTimeout      = 5 * time.Second
)

// minDefaultMaxConns is the floor of the default MaxConns, so a single-core
// container still gets a usable pool.
const minDefaultMaxConns = 4

// PoolConfig is how a Pool opens its connections and how many it keeps.
//
// The fields are named as nextar's PostgreSQL pool names them, and as pgxpool
// did: MaxConns, MinConns, MaxConnLifetime, MaxConnIdleTime,
// HealthCheckPeriod, AfterConnect, ResetQuery. A negative duration turns that
// behaviour off.
type PoolConfig struct {
	// ConnConfig is how each connection is opened. The pool opens every one
	// from a copy, so changing it after NewPoolConfig changes nothing.
	ConnConfig *wire.Config
	// Options are each connection's: its statement cache, result formats and
	// tracer. Behind a pooler in transaction mode that does not track prepared
	// statements, set StatementCacheCapacity to -1. nextar's gpoolproxy and
	// PgBouncer 1.21 and later track them, and the default cache works there.
	Options Options

	// MaxConns bounds the connections open at once, in use or idle. Zero means
	// 4 or GOMAXPROCS, whichever is more.
	MaxConns int32
	// MinConns is how many connections the pool keeps open while idle, opened
	// in the background. Zero keeps none: connections are opened as they are
	// asked for.
	MinConns int32
	// MaxConnLifetime closes a connection this long after it was opened, when
	// it is next idle. It is what moves a pool to a new primary after a
	// failover or a DNS change. Zero means DefaultPoolMaxConnLifetime.
	MaxConnLifetime time.Duration
	// MaxConnIdleTime closes a connection left idle this long. Zero means
	// DefaultPoolMaxConnIdleTime.
	MaxConnIdleTime time.Duration
	// HealthCheckPeriod is how often idle connections past their lifetime or
	// idle time are closed, and the pool topped up to MinConns. Zero means
	// DefaultPoolHealthCheckPeriod.
	HealthCheckPeriod time.Duration

	// AfterConnect runs on each new connection before it is first handed out,
	// for a SET or a LISTEN. An error closes the connection and fails the
	// acquire that opened it.
	AfterConnect func(ctx context.Context, conn *Conn) error
	// ResetQuery runs on every connection given back, such as "DISCARD ALL"
	// to drop session state one caller left for the next. It costs a round
	// trip on every release; a connection whose reset fails is closed. A reset
	// that discards prepared statements must be paired with
	// Options.StatementCacheCapacity = -1, or the cache would name statements
	// the server no longer has.
	ResetQuery string
	// ResetTimeout bounds what is done to a connection given back: the
	// rollback of a transaction left open, and ResetQuery. Zero means
	// DefaultPoolResetTimeout.
	ResetTimeout time.Duration
}

// The connection string settings ParsePoolConfig reads, named as pgxpool and
// nextar name them, so a string written for either works here. wire.ParseConfig
// drops them, so none is sent to the server.
const (
	keyPoolMaxConns          = "pool_max_conns"
	keyPoolMinConns          = "pool_min_conns"
	keyPoolMaxConnLifetime   = "pool_max_conn_lifetime"
	keyPoolMaxConnIdleTime   = "pool_max_conn_idle_time"
	keyPoolHealthCheckPeriod = "pool_health_check_period"
)

// ParsePoolConfig parses a connection string as wire.ParseConfig does, and
// reads pool_max_conns, pool_min_conns, pool_max_conn_lifetime,
// pool_max_conn_idle_time and pool_health_check_period from it as pgxpool
// did. Durations are Go durations, such as 30m. The rest of the pool's
// settings are left at zero, for the defaults.
func ParsePoolConfig(connString string) (*PoolConfig, error) {
	conn, err := wire.ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	settings, err := wire.Settings(connString)
	if err != nil {
		return nil, err
	}
	config := &PoolConfig{ConnConfig: conn}
	for key, dst := range map[string]*int32{keyPoolMaxConns: &config.MaxConns, keyPoolMinConns: &config.MinConns} {
		if v, ok := settings[key]; ok {
			n, err := strconv.ParseInt(v, 10, 32)
			if err != nil || n < 0 || (key == keyPoolMaxConns && n == 0) {
				return nil, fmt.Errorf("%w: %s is not a number of connections", wire.ErrInvalidConnString, key)
			}
			*dst = int32(n)
		}
	}
	for key, dst := range map[string]*time.Duration{
		keyPoolMaxConnLifetime: &config.MaxConnLifetime, keyPoolMaxConnIdleTime: &config.MaxConnIdleTime,
		keyPoolHealthCheckPeriod: &config.HealthCheckPeriod,
	} {
		if v, ok := settings[key]; ok {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return nil, fmt.Errorf("%w: %s is not a duration", wire.ErrInvalidConnString, key)
			}
			*dst = d
		}
	}
	return config, nil
}

// Copy returns a copy of the configuration, with its own ConnConfig, for a
// second pool that differs from the first, as one with another search_path
// does.
func (c *PoolConfig) Copy() *PoolConfig {
	clone := *c
	if c.ConnConfig != nil {
		clone.ConnConfig = c.ConnConfig.Copy()
	}
	return &clone
}

// withDefaults fills in what was left at zero, and turns a negative duration
// into zero, which the pool reads as no limit.
func (c PoolConfig) withDefaults() PoolConfig {
	if c.MaxConns == 0 {
		c.MaxConns = int32(max(minDefaultMaxConns, runtime.GOMAXPROCS(0)))
	}
	c.MaxConnLifetime = orDefault(c.MaxConnLifetime, DefaultPoolMaxConnLifetime)
	c.MaxConnIdleTime = orDefault(c.MaxConnIdleTime, DefaultPoolMaxConnIdleTime)
	c.HealthCheckPeriod = orDefault(c.HealthCheckPeriod, DefaultPoolHealthCheckPeriod)
	c.ResetTimeout = orDefault(c.ResetTimeout, DefaultPoolResetTimeout)
	return c
}

func orDefault(d, fallback time.Duration) time.Duration {
	switch {
	case d == 0:
		return fallback
	case d < 0:
		return 0
	}
	return d
}

func (c PoolConfig) validate() error {
	switch {
	case c.ConnConfig == nil:
		return fmt.Errorf("%w: PoolConfig.ConnConfig is nil", ErrPoolConfig)
	case c.MaxConns < 1:
		return fmt.Errorf("%w: MaxConns is %d", ErrPoolConfig, c.MaxConns)
	case c.MinConns < 0 || c.MinConns > c.MaxConns:
		return fmt.Errorf("%w: MinConns %d is not between 0 and MaxConns %d", ErrPoolConfig, c.MinConns, c.MaxConns)
	}
	return nil
}
