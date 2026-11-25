// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

//go:build linux

package caduceus

import (
	"context"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// lowWater reads the socket's receive low-water mark, or skips the test where
// the connection is not TCP.
func lowWater(t *testing.T, conn *Conn) int {
	t.Helper()
	tcp, ok := conn.Wire().NetConn().(*net.TCPConn)
	if !ok {
		t.Skip("not a TCP connection")
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	mark := 0
	if err := raw.Control(func(fd uintptr) {
		mark, err = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVLOWAT)
	}); err != nil {
		t.Fatal(err)
	}
	return mark
}

// modernKernel reports whether the kernel is Linux 5 or later, comfortably
// one whose reader is woken when the mark is lowered, by the release it
// reports, as the driver judges it.
func modernKernel(t *testing.T) bool {
	t.Helper()
	var name syscall.Utsname
	if err := syscall.Uname(&name); err != nil {
		t.Fatal(err)
	}
	major := 0
	for _, c := range name.Release {
		if c < '0' || c > '9' {
			break
		}
		major = major*10 + int(c-'0')
	}
	return major >= 5
}

// NoStreamWatermark leaves the mark alone, however long the result.
func TestLiveNoStreamWatermarkLeavesTheMarkAlone(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	config, err := wire.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	config.NoStreamWatermark = true
	conn, err := ConnectConfig(t.Context(), config, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	rows, err := conn.Query(t.Context(), "SELECT repeat('x', 100) FROM generate_series(1, 200000)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for n := 0; rows.Next(); n++ {
		if n%1000 == 0 && lowWater(t, conn) != 1 {
			t.Fatalf("row %d: the low-water mark is %d with NoStreamWatermark set", n, lowWater(t, conn))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// A result long enough to have the socket's low-water mark raised under it
// still delivers every row, and the mark is back down when it ends, so the
// next short reply on the connection is not held for 64 KiB that never come.
func TestLiveLongResultLowersTheWatermarkItRaised(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	const want = 300000
	rows, err := conn.Query(ctx, "SELECT g, repeat('x', 100) FROM generate_series(1, $1::int4) AS g", int32(want))
	if err != nil {
		t.Fatal(err)
	}
	raised, n := false, 0
	for rows.Next() {
		var g int64
		var filler string
		if err := rows.Scan(&g, &filler); err != nil || g != int64(n+1) || len(filler) != 100 {
			t.Fatalf("row %d = %d, %d bytes, %v", n, g, len(filler), err)
		}
		n++
		if n%1000 == 0 && lowWater(t, conn) > 1 {
			raised = true
		}
	}
	if err := rows.Err(); err != nil || n != want {
		t.Fatalf("read %d rows of %d, %v", n, want, err)
	}
	if !raised && modernKernel(t) {
		// It used to be only logged, so a mark never raised at all passed.
		t.Fatal("the low-water mark was never seen raised under a long result")
	}
	if mark := lowWater(t, conn); mark != 1 {
		t.Fatalf("low-water mark after the result = %d, want 1", mark)
	}
	// A short reply straight after must not wait on anything.
	start := time.Now()
	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("SELECT 1 after a long result = %d, %v", one, err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("SELECT 1 after a long result took %v", took)
	}
}

// With the mark raised, rows the server has already sent sit below it when the
// server pauses. They must reach the caller while it pauses, not when it
// resumes: a mark left raised would hold up to 64 KiB of rows, some 500 of
// these, until more arrived behind them.
func TestLiveResultThatPausesDeliversWhatWasSent(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	const pauseAt, total = 200000, 201000
	rows, err := conn.Query(ctx, `SELECT g, repeat('x', 100), CASE WHEN g = $1::int4 THEN pg_sleep(0.5) END
		FROM generate_series(1, $2::int4) AS g`, int32(pauseAt), int32(total))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	resumed, n, last := 0, 0, time.Now()
	for rows.Next() {
		n++
		if now := time.Now(); now.Sub(last) > 250*time.Millisecond && resumed == 0 {
			resumed = n
		} else {
			last = now
		}
		if resumed != 0 {
			last = time.Now()
		}
	}
	if err := rows.Err(); err != nil || n != total {
		t.Fatalf("read %d rows of %d, %v", n, total, err)
	}
	// The server keeps up to one 8 KiB buffer, about 70 rows, unsent while it
	// sleeps. Anything further back than that had been sent.
	if resumed == 0 || resumed < pauseAt-150 {
		t.Fatalf("the first row after the pause was %d; rows sent before row %d were held back", resumed, pauseAt)
	}
}
