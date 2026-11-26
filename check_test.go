// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// CheckConn finds, with no round trip, a session the server ended, which
// it says with a FATAL 57P01, and closes the connection. A live idle one
// passes, Ping makes a round trip on it, and a notification that arrived
// while it was idle is kept, not taken for an error.
func TestLiveCheckConn(t *testing.T) {
	conn := liveConn(t, Options{})
	other := liveConn(t, Options{})
	ctx := t.Context()
	if err := conn.CheckConn(); err != nil {
		t.Fatalf("CheckConn on a live connection = %v", err)
	}
	if err := conn.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := conn.Exec(ctx, "LISTEN caduceus_check"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec(ctx, "NOTIFY caduceus_check, 'idle'"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := conn.CheckConn(); err != nil {
		t.Fatalf("CheckConn with a notification waiting = %v", err)
	}
	if notes := conn.Wire().Notifications(); len(notes) != 1 || notes[0].Payload != "idle" {
		t.Fatalf("the notification read by CheckConn = %v", notes)
	}

	pid := conn.Wire().PID()
	var ended bool
	if err := other.QueryRow(ctx, "SELECT pg_terminate_backend($1)", int32(pid)).Scan(&ended); err != nil || !ended {
		t.Fatalf("pg_terminate_backend = %v, %v", ended, err)
	}
	time.Sleep(50 * time.Millisecond)
	err := conn.CheckConn()
	var server *wire.Error
	if !errors.As(err, &server) || server.Code != "57P01" {
		t.Fatalf("CheckConn after the session was ended = %v, want 57P01", err)
	}
	if !conn.IsClosed() {
		t.Fatal("CheckConn left an ended session open")
	}
}

// Notices and notifications go to the Config's handlers when it has them,
// and a notification handed to one is not also queued.
func TestLiveNoticeAndNotificationHandlers(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	config, err := wire.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	var notices []string
	var notifications []wire.Notification
	config.OnNotice = func(notice *wire.Error) { notices = append(notices, notice.Severity+": "+notice.Message) }
	config.OnNotification = func(n wire.Notification) { notifications = append(notifications, n) }
	ctx := t.Context()
	conn, err := ConnectConfig(ctx, config, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "DO $$ BEGIN RAISE NOTICE 'hello %', 42; END $$"); err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0] != "NOTICE: hello 42" {
		t.Fatalf("notices = %q", notices)
	}
	if _, err := conn.Exec(ctx, "LISTEN caduceus_handler; NOTIFY caduceus_handler, 'p'"); err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0].Payload != "p" || len(conn.Wire().Notifications()) != 0 {
		t.Fatalf("handled %v, queued %v", notifications, conn.Wire().Notifications())
	}
}
