// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

func TestParsePoolConfig(t *testing.T) {
	config, err := ParsePoolConfig("postgres://u:p@db:5432/app?sslmode=disable&pool_max_conns=7&pool_min_conns=2" +
		"&pool_max_conn_lifetime=90m&pool_max_conn_idle_time=5m&pool_health_check_period=30s")
	if err != nil {
		t.Fatalf("ParsePoolConfig() = %v", err)
	}
	if config.MaxConns != 7 || config.MinConns != 2 || config.MaxConnLifetime != 90*time.Minute ||
		config.MaxConnIdleTime != 5*time.Minute || config.HealthCheckPeriod != 30*time.Second {
		t.Fatalf("ParsePoolConfig() = %+v", config)
	}
	if _, ok := config.ConnConfig.RuntimeParams["pool_max_conns"]; ok {
		t.Fatal("pool_max_conns would be sent to the server")
	}
	config, err = ParsePoolConfig("host=db user=u dbname=app sslmode=disable pool_max_conns=3")
	if err != nil || config.MaxConns != 3 {
		t.Fatalf("keyword form: %+v, %v", config, err)
	}
	for _, bad := range []string{"pool_max_conns=0", "pool_max_conns=x", "pool_min_conns=-1", "pool_max_conn_lifetime=1"} {
		if _, err := ParsePoolConfig("postgres://u@db/app?sslmode=disable&" + bad); !errors.Is(err, wire.ErrInvalidConnString) {
			t.Errorf("%s: err = %v, want ErrInvalidConnString", bad, err)
		}
	}
}

func TestPoolConfigDefaults(t *testing.T) {
	c := PoolConfig{MaxConnIdleTime: -1}.withDefaults()
	if c.MaxConns < minDefaultMaxConns || c.MaxConnLifetime != DefaultPoolMaxConnLifetime ||
		c.MaxConnIdleTime != 0 || c.HealthCheckPeriod != DefaultPoolHealthCheckPeriod {
		t.Fatalf("withDefaults() = %+v", c)
	}
	if _, err := NewPoolConfig(context.Background(), &PoolConfig{}); !errors.Is(err, ErrPoolConfig) {
		t.Fatalf("no ConnConfig: err = %v", err)
	}
	conn, _ := wire.ParseConfig("postgres://u@db/app?sslmode=disable")
	if _, err := NewPoolConfig(context.Background(), &PoolConfig{ConnConfig: conn, MaxConns: 2, MinConns: 3}); !errors.Is(err, ErrPoolConfig) {
		t.Fatalf("MinConns > MaxConns: err = %v", err)
	}
}

// livePool opens a pool on DATABASE_URL, or on url when it is set.
func livePool(t *testing.T, url string, change func(*PoolConfig)) *Pool {
	t.Helper()
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	config, err := ParsePoolConfig(url)
	if err != nil {
		t.Fatalf("ParsePoolConfig() = %v", err)
	}
	if change != nil {
		change(config)
	}
	pool, err := NewPoolConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("NewPoolConfig() = %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// idleSoon waits for the pool to have no connection in use.
func idleSoon(t *testing.T, pool *Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for pool.Stat().AcquiredConns() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d connections still in use", pool.Stat().AcquiredConns())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLivePoolGivesConnectionsBack(t *testing.T) {
	pool := livePool(t, "", func(c *PoolConfig) { c.MaxConns = 1 })
	ctx := t.Context()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping() = %v", err)
	}
	// With one connection, each of these hangs unless the one before gave
	// it back.
	for range 3 {
		var n int
		if err := pool.QueryRow(ctx, "SELECT $1::int", 7).Scan(&n); err != nil || n != 7 {
			t.Fatalf("QueryRow = %d, %v", n, err)
		}
		if err := pool.QueryRow(ctx, "SELECT 1 WHERE false").Scan(&n); !errors.Is(err, ErrNoRows) {
			t.Fatalf("no row: %v", err)
		}
		if err := pool.QueryRow(ctx, "SELECT nonsense").Scan(&n); err == nil {
			t.Fatal("a bad statement scanned")
		}
		got, err := CollectRows(must(pool.Query(ctx, "SELECT generate_series(1, 3)")), RowTo[int])
		if err != nil || len(got) != 3 {
			t.Fatalf("CollectRows = %v, %v", got, err)
		}
		rows, err := pool.Query(ctx, "SELECT generate_series(1, 100)")
		if err != nil {
			t.Fatalf("Query() = %v", err)
		}
		rows.Next()
		rows.Close() // closed half read
		if _, err := pool.Query(ctx, "SELECT nonsense"); err == nil {
			t.Fatal("a bad query ran")
		}
		if _, err := pool.Exec(ctx, "SELECT 1"); err != nil {
			t.Fatalf("Exec() = %v", err)
		}
		results, err := pool.SendBatch(ctx, []Query{{SQL: "SELECT 1"}, {SQL: "SELECT 2"}})
		if err != nil {
			t.Fatalf("SendBatch() = %v", err)
		}
		if err := results.Close(); err != nil {
			t.Fatalf("Close() = %v", err)
		}
		err = BeginFunc(ctx, pool, func(tx *Tx) error {
			_, err := tx.Exec(ctx, "SELECT 1")
			return err
		})
		if err != nil {
			t.Fatalf("BeginFunc() = %v", err)
		}
		if err := BeginFunc(ctx, pool, func(*Tx) error { return errors.New("no") }); err == nil {
			t.Fatal("BeginFunc ignored the error")
		}
		c, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatalf("Acquire() = %v", err)
		}
		c.Release()
		c.Release()
	}
	idleSoon(t, pool)
	if st := pool.Stat(); st.TotalConns() != 1 || st.NewConnsCount() != 1 {
		t.Fatalf("TotalConns %d NewConnsCount %d, want one connection reused", st.TotalConns(), st.NewConnsCount())
	}
}

func TestLivePoolBoundsConnections(t *testing.T) {
	pool := livePool(t, "", func(c *PoolConfig) { c.MaxConns = 3 })
	ctx := t.Context()
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for range 40 {
		wg.Go(func() {
			if _, err := pool.Exec(ctx, "SELECT pg_sleep(0.01)"); err != nil {
				errs <- err
			}
			if pool.Stat().TotalConns() > 3 {
				errs <- errors.New("more than MaxConns open")
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if n := pool.Stat().TotalConns(); n > 3 {
		t.Fatalf("TotalConns = %d", n)
	}
}

func TestLivePoolUnwindsWhatIsLeftOpen(t *testing.T) {
	pool := livePool(t, "", func(c *PoolConfig) { c.MaxConns = 1 })
	ctx := t.Context()
	c, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire() = %v", err)
	}
	tx, err := c.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}
	if _, err := tx.Exec(ctx, "CREATE TEMP TABLE left_open (id int)"); err != nil {
		t.Fatalf("Exec() = %v", err)
	}
	c.Release()
	if err := tx.Commit(ctx); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("a Tx outlived its release: Commit() = %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, "SELECT count(*)::text FROM pg_class WHERE relname = 'left_open'").Scan(&status); err != nil || status != "0" {
		t.Fatalf("the transaction was not rolled back: %q, %v", status, err)
	}
}

func TestLivePoolResetAndAfterConnect(t *testing.T) {
	connects := 0
	pool := livePool(t, "", func(c *PoolConfig) {
		c.MaxConns = 1
		c.ResetQuery = "RESET ALL"
		c.AfterConnect = func(ctx context.Context, conn *Conn) error {
			connects++
			_, err := conn.Exec(ctx, "SET application_name = 'pooled'")
			return err
		}
	})
	ctx := t.Context()
	if _, err := pool.Exec(ctx, "SET work_mem = '1234kB'"); err != nil {
		t.Fatalf("SET = %v", err)
	}
	var workMem string
	if err := pool.QueryRow(ctx, "SHOW work_mem").Scan(&workMem); err != nil || workMem == "1234kB" {
		t.Fatalf("ResetQuery did not run: %q, %v", workMem, err)
	}
	if connects != 1 {
		t.Fatalf("AfterConnect ran %d times", connects)
	}
}

func TestLivePoolWaitsAndCloses(t *testing.T) {
	pool := livePool(t, "", func(c *PoolConfig) { c.MaxConns = 1 })
	ctx := t.Context()
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire() = %v", err)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := pool.Exec(short, "SELECT 1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Exec on a full pool = %v, want DeadlineExceeded", err)
	}
	if pool.Stat().CanceledAcquireCount() != 1 || pool.Stat().EmptyAcquireCount() != 1 {
		t.Fatalf("Stat() = %+v", pool.Stat())
	}
	done := make(chan error, 1)
	go func() { _, err := pool.Exec(ctx, "SELECT 1"); done <- err }()
	time.Sleep(20 * time.Millisecond)
	held.Release()
	if err := <-done; err != nil {
		t.Fatalf("a waiter was not handed the released connection: %v", err)
	}
	pool.Close()
	if _, err := pool.Exec(ctx, "SELECT 1"); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("Exec after Close = %v", err)
	}
	if n := pool.Stat().TotalConns(); n != 0 {
		t.Fatalf("Close left %d open", n)
	}
}

func TestLivePoolMinConnsAndLifetime(t *testing.T) {
	pool := livePool(t, "", func(c *PoolConfig) {
		c.MaxConns, c.MinConns = 3, 2
		c.HealthCheckPeriod = 20 * time.Millisecond
		c.MaxConnLifetime = 100 * time.Millisecond
	})
	deadline := time.Now().Add(5 * time.Second)
	for pool.Stat().TotalConns() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("MinConns were not opened")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for pool.Stat().MaxLifetimeDestroyCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no connection was closed for its lifetime")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestLivePoolThroughPooler runs a pool through a pooler in transaction mode,
// such as nextar's gpoolproxy (github.com/gsoultan/nextar,
// examples/gpoolproxy) or PgBouncer, named by POOLER_URL. More
// client connections than the pooler has server connections each prepare
// statements and run transactions, so a statement or a transaction that
// leaked from one server connection to another would show.
func TestLivePoolThroughPooler(t *testing.T) {
	url := os.Getenv("POOLER_URL")
	if url == "" {
		t.Skip("POOLER_URL not set")
	}
	for _, options := range []Options{{}, {StatementCacheCapacity: -1}} {
		pool := livePool(t, url, func(c *PoolConfig) { c.MaxConns, c.Options = 12, options })
		ctx := t.Context()
		var wg sync.WaitGroup
		errs := make(chan error, 12*20)
		for worker := range 12 {
			wg.Go(func() {
				for i := range 20 {
					var n int
					// Statements of different texts, so each connection's
					// cache names several, through server connections it
					// does not keep.
					sql := []string{"SELECT $1::int + 0", "SELECT $1::int + 0 -- b", "SELECT $1::int + 0 -- c"}[i%3]
					if err := pool.QueryRow(ctx, sql, worker*100+i).Scan(&n); err != nil {
						errs <- err
						return
					} else if n != worker*100+i {
						errs <- errors.New("a result came back for another statement")
						return
					}
					err := BeginFunc(ctx, pool, func(tx *Tx) error {
						if _, err := tx.Exec(ctx, "SET LOCAL application_name = 'w'"); err != nil {
							return err
						}
						var name string
						if err := tx.QueryRow(ctx, "SELECT current_setting('application_name')").Scan(&name); err != nil {
							return err
						}
						if name != "w" {
							return errors.New("a transaction ran on two server connections")
						}
						return nil
					})
					if err != nil {
						errs <- err
						return
					}
				}
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("StatementCacheCapacity %d: %v", options.StatementCacheCapacity, err)
		}
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
