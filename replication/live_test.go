// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// Tests against a real server, skipped unless DATABASE_URL is set. It needs
// wal_level=logical and a role that may replicate.
func liveConn(t *testing.T, replication string) *wire.Conn {
	t.Helper()
	conn, err := wire.Connect(t.Context(), liveConfig(t, replication))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func liveConfig(t *testing.T, replication string) *wire.Config {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	config, err := wire.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if replication != "" {
		config.RuntimeParams["replication"] = replication
	}
	return config
}

// A logical slot streams every kind of pgoutput message a transaction makes,
// and each parses: Begin, Relation, Insert, Truncate, Message, Commit.
func TestLiveLogicalReplication(t *testing.T) {
	sql := liveConn(t, "")
	walsender := liveConn(t, "database")
	ctx := t.Context()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	table, publication, slot := "caduceus_repl_"+suffix, "caduceus_pub_"+suffix, "caduceus_slot_"+suffix
	if _, err := sql.Exec(ctx, "CREATE TABLE "+table+" (id int PRIMARY KEY, note text); CREATE PUBLICATION "+publication+" FOR TABLE "+table); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = sql.Exec(context.Background(), "DROP PUBLICATION IF EXISTS "+publication+"; DROP TABLE IF EXISTS "+table)
	})

	identity, err := IdentifySystem(ctx, walsender)
	if err != nil || identity.Database != "postgres" || identity.XLogPos == 0 || identity.Timeline < 1 {
		t.Fatalf("IdentifySystem = %+v, %v", identity, err)
	}
	created, err := CreateReplicationSlot(ctx, walsender, slot, "pgoutput", SlotOptions{Temporary: true, Snapshot: "noexport"})
	if err != nil || created.Name != slot || created.ConsistentPoint == 0 || created.Plugin != "pgoutput" || created.SnapshotName != "" {
		t.Fatalf("CreateReplicationSlot = %+v, %v", created, err)
	}

	if _, err := sql.Exec(ctx, "BEGIN; INSERT INTO "+table+" VALUES (1, 'it''s'); SELECT pg_logical_emit_message(true, 'caduceus', 'hello'); TRUNCATE "+table+"; COMMIT"); err != nil {
		t.Fatal(err)
	}
	args := PgoutputOptions{ProtoVersion: 2, Publications: []string{publication}, Messages: true}.Args()
	if err := StartReplication(ctx, walsender, slot, 0, StartOptions{PluginArgs: args}); err != nil {
		t.Fatal(err)
	}
	if err := SendStandbyStatusUpdate(ctx, walsender, StandbyStatusUpdate{WALWritePosition: created.ConsistentPoint, WALFlushPosition: created.ConsistentPoint, WALApplyPosition: created.ConsistentPoint, ReplyRequested: true}); err != nil {
		t.Fatal(err)
	}

	var kinds []byte
	keepalive := false
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for len(kinds) == 0 || kinds[len(kinds)-1] != msgCommit {
		if deadline.Err() != nil {
			t.Fatalf("messages so far %q", kinds)
		}
		_ = walsender.NetConn().SetReadDeadline(time.Now().Add(5 * time.Second))
		kind, body, err := walsender.Receive()
		if err != nil {
			t.Fatal(err)
		}
		if kind != wire.MsgCopyData || len(body) == 0 {
			continue
		}
		if body[0] == PrimaryKeepalive {
			keepalive = true
			continue
		}
		record, err := ParseXLogData(body)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseMessage(record.Data, false)
		if err != nil {
			t.Fatalf("ParseMessage(%q) = %v", record.Data[0], err)
		}
		kinds = append(kinds, parsed.Type())
		check(t, parsed, table)
		// Before PostgreSQL 15, pgoutput sends every other transaction too,
		// as a Begin and a Commit with nothing between: whatever else ran on
		// the server meanwhile. It is not the one under test.
		if string(kinds) == "BC" {
			kinds = kinds[:0]
		}
	}
	// pgoutput sends the relation again ahead of the truncate.
	if string(kinds) != "BRIMRTC" {
		t.Errorf("messages %q, want Begin, Relation, Insert, Message, Relation, Truncate, Commit", kinds)
	}
	if !keepalive {
		t.Error("a status update that asked for a reply got none")
	}
}

// check looks at what a message says, for the messages of the test's
// transaction.
func check(t *testing.T, parsed Message, table string) {
	t.Helper()
	switch m := parsed.(type) {
	case *RelationMessage:
		if m.RelationName != table || len(m.Columns) != 2 || m.Columns[0].DataType != 23 || m.Columns[0].Flags != 1 {
			t.Errorf("relation = %+v", m)
		}
	case *InsertMessage:
		if string(m.Tuple.Columns[1].Data) != "it's" {
			t.Errorf("insert = %+v", m.Tuple)
		}
	case *LogicalDecodingMessage:
		if m.Prefix != "caduceus" || string(m.Content) != "hello" || !m.Transactional {
			t.Errorf("message = %+v", m)
		}
	case *TruncateMessage:
		if len(m.RelationIDs) != 1 {
			t.Errorf("truncate = %+v", m)
		}
	case *CommitMessage:
		if m.TransactionEndLSN <= m.CommitLSN {
			t.Errorf("commit = %+v", m)
		}
	}
}

// A physical slot streams WAL, and a slot that is not temporary is there
// until it is dropped.
func TestLivePhysicalReplicationAndDrop(t *testing.T) {
	walsender := liveConn(t, "true")
	ctx := t.Context()
	slot := "caduceus_phys_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := CreateReplicationSlot(ctx, walsender, slot, "", SlotOptions{Physical: true, ReserveWAL: true}); err != nil {
		t.Fatal(err)
	}
	config := liveConfig(t, "true")
	t.Cleanup(func() {
		ctx := context.Background()
		if conn, err := wire.Connect(ctx, config); err == nil {
			_ = DropReplicationSlot(ctx, conn, slot, false)
			_ = conn.Close(ctx)
		}
	})
	// READ_REPLICATION_SLOT reads physical slots only, and came with
	// PostgreSQL 15: an older walsender refuses it as an SQL command.
	if serverMajor(t, walsender) >= 15 {
		if state, err := ReadReplicationSlot(ctx, walsender, slot); err != nil || !state.Exists || state.Type != "physical" || state.RestartLSN == 0 {
			t.Fatalf("ReadReplicationSlot = %+v, %v", state, err)
		}
		if state, err := ReadReplicationSlot(ctx, walsender, "caduceus_no_such_slot"); err != nil || state.Exists {
			t.Fatalf("ReadReplicationSlot of a missing slot = %+v, %v", state, err)
		}
	}
	identity, err := IdentifySystem(ctx, walsender)
	if err != nil || identity.Database != "" {
		t.Fatalf("IdentifySystem on a physical connection = %+v, %v", identity, err)
	}
	if _, err := TimelineHistoryFile(ctx, walsender, identity.Timeline); err == nil && identity.Timeline == 1 {
		t.Error("timeline 1 has no history file, and one was returned")
	}
	if err := StartReplication(ctx, walsender, slot, identity.XLogPos, StartOptions{Physical: true}); err != nil {
		t.Fatal(err)
	}
	// A status update that asks for a reply is answered at once.
	if err := SendStandbyStatusUpdate(ctx, walsender, StandbyStatusUpdate{WALWritePosition: identity.XLogPos, ReplyRequested: true}); err != nil {
		t.Fatal(err)
	}
	_ = walsender.NetConn().SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, body, err := walsender.Receive()
	if err != nil || kind != wire.MsgCopyData || (body[0] != XLogData && body[0] != PrimaryKeepalive) {
		t.Fatalf("first message %q, %q, %v", kind, body[:1], err)
	}

	// A slot in use is refused without wait, and waited for with it.
	dropper := liveConn(t, "true")
	if err := DropReplicationSlot(ctx, dropper, slot, false); !isCode(err, "55006") {
		t.Fatalf("DropReplicationSlot of a slot in use = %v, want 55006", err)
	}
	dropped := make(chan error, 1)
	go func() { dropped <- DropReplicationSlot(ctx, dropper, slot, true) }()
	select {
	case err := <-dropped:
		t.Fatalf("DropReplicationSlot with wait returned %v while the slot was in use", err)
	case <-time.After(300 * time.Millisecond):
	}
	_ = walsender.Close(ctx)
	if err := <-dropped; err != nil {
		t.Fatal(err)
	}
	if serverMajor(t, dropper) >= 15 {
		if state, err := ReadReplicationSlot(ctx, dropper, slot); err != nil || state.Exists {
			t.Fatalf("after DropReplicationSlot: %+v, %v", state, err)
		}
	}
}

func isCode(err error, code string) bool {
	var server *wire.Error
	return errors.As(err, &server) && server.Code == code
}

// serverMajor is the server's major version, from the server_version it
// reported at login.
func serverMajor(t *testing.T, conn *wire.Conn) int {
	t.Helper()
	major, _, _ := strings.Cut(conn.ParameterStatus("server_version"), ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		t.Fatalf("server_version %q: %v", conn.ParameterStatus("server_version"), err)
	}
	return n
}
