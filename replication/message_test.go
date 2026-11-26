// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// built is a message assembled field by field, as the server sends it.
type built []byte

func message(kind byte) built        { return built{kind} }
func (b built) u8(v byte) built      { return append(b, v) }
func (b built) u16(v uint16) built   { return binary.BigEndian.AppendUint16(b, v) }
func (b built) u32(v uint32) built   { return binary.BigEndian.AppendUint32(b, v) }
func (b built) u64(v uint64) built   { return binary.BigEndian.AppendUint64(b, v) }
func (b built) str(v string) built   { return append(append(b, v...), 0) }
func (b built) raw(v []byte) built   { return append(b, v...) }
func (b built) at(t time.Time) built { return b.u64(uint64(t.Sub(postgresEpoch).Microseconds())) }

// Every message of protocol versions 1 to 4 is parsed into its type, with
// the Xid a stream puts first where it does.
func TestParseMessage(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	row := tuple(textColumn("7"), nullColumn, unchangedColumn)
	wantRow := &TupleData{Columns: []TupleColumn{{Kind: TupleText, Data: []byte("7")}, {Kind: TupleNull}, {Kind: TupleUnchanged}}}
	tests := []struct {
		name     string
		record   built
		inStream bool
		want     Message
	}{
		{"begin", message('B').u64(100).at(when).u32(9), false, &BeginMessage{FinalLSN: 100, CommitTime: when, Xid: 9}},
		{"commit", message('C').u8(0).u64(100).u64(120).at(when), false, &CommitMessage{CommitLSN: 100, TransactionEndLSN: 120, CommitTime: when}},
		{"origin", message('O').u64(50).str("node_a"), false, &OriginMessage{CommitLSN: 50, Name: "node_a"}},
		{"relation", message('R').u32(16384).str("public").str("users").u8('f').u16(1).u8(1).str("id").u32(23).u32(0xFFFFFFFF), false,
			&RelationMessage{RelationID: 16384, Namespace: "public", RelationName: "users", ReplicaIdentity: 'f', Columns: []RelationColumn{{Flags: 1, Name: "id", DataType: 23, TypeModifier: -1}}}},
		{"relation in a stream", message('R').u32(77).u32(1).str("s").str("t").u8('d').u16(0), true,
			&RelationMessage{Xid: 77, RelationID: 1, Namespace: "s", RelationName: "t", ReplicaIdentity: 'd', Columns: []RelationColumn{}}},
		{"type", message('Y').u32(90000).str("public").str("mood"), false, &TypeMessage{DataType: 90000, Namespace: "public", Name: "mood"}},
		{"insert", message('I').u32(1).u8('N').raw(row), false, &InsertMessage{RelationID: 1, Tuple: wantRow}},
		{"update with the old row", message('U').u32(1).u8('O').raw(row).u8('N').raw(row), false, &UpdateMessage{RelationID: 1, OldTupleType: TupleOld, OldTuple: wantRow, NewTuple: wantRow}},
		{"update without", message('U').u32(1).u8('N').raw(row), false, &UpdateMessage{RelationID: 1, NewTuple: wantRow}},
		{"delete", message('D').u32(1).u8('K').raw(row), false, &DeleteMessage{RelationID: 1, OldTupleType: TupleKey, OldTuple: wantRow}},
		{"delete in a stream", message('D').u32(5).u32(1).u8('K').raw(row), true, &DeleteMessage{Xid: 5, RelationID: 1, OldTupleType: TupleKey, OldTuple: wantRow}},
		{"truncate", message('T').u32(2).u8(3).u32(10).u32(11), false, &TruncateMessage{Option: 3, RelationIDs: []uint32{10, 11}}},
		{"message", message('M').u8(1).u64(300).str("app").u32(5).raw([]byte("hello")), false, &LogicalDecodingMessage{Transactional: true, LSN: 300, Prefix: "app", Content: []byte("hello")}},
		{"message in a stream", message('M').u32(8).u8(0).u64(300).str("app").u32(0), true, &LogicalDecodingMessage{Xid: 8, LSN: 300, Prefix: "app", Content: []byte{}}},
		{"stream start", message('S').u32(8).u8(1), false, &StreamStartMessage{Xid: 8, FirstSegment: true}},
		{"stream stop", message('E'), true, &StreamStopMessage{}},
		{"stream commit", message('c').u32(8).u8(0).u64(400).u64(410).at(when), false, &StreamCommitMessage{Xid: 8, CommitLSN: 400, TransactionEndLSN: 410, CommitTime: when}},
		{"stream abort", message('A').u32(8).u32(9), false, &StreamAbortMessage{Xid: 8, SubXid: 9}},
		{"stream abort, version 4", message('A').u32(8).u32(8).u64(500).at(when), false, &StreamAbortMessage{Xid: 8, SubXid: 8, AbortLSN: 500, AbortTime: when}},
		{"begin prepare", message('b').u64(600).u64(610).at(when).u32(3).str("gid1"), false, &BeginPrepareMessage{PrepareLSN: 600, EndPrepareLSN: 610, PrepareTime: when, Xid: 3, Gid: "gid1"}},
		{"prepare", message('P').u8(0).u64(600).u64(610).at(when).u32(3).str("gid1"), false, &PrepareMessage{PrepareLSN: 600, EndPrepareLSN: 610, PrepareTime: when, Xid: 3, Gid: "gid1"}},
		{"stream prepare", message('p').u8(0).u64(600).u64(610).at(when).u32(3).str("gid1"), false, &PrepareMessage{PrepareLSN: 600, EndPrepareLSN: 610, PrepareTime: when, Xid: 3, Gid: "gid1", Streamed: true}},
		{"commit prepared", message('K').u8(0).u64(700).u64(710).at(when).u32(3).str("gid1"), false, &CommitPreparedMessage{CommitLSN: 700, EndCommitLSN: 710, CommitTime: when, Xid: 3, Gid: "gid1"}},
		{"rollback prepared", message('r').u8(0).u64(800).u64(810).at(when).at(when.Add(time.Second)).u32(3).str("gid1"), false,
			&RollbackPreparedMessage{EndLSN: 800, RollbackLSN: 810, PrepareTime: when, RollbackTime: when.Add(time.Second), Xid: 3, Gid: "gid1"}},
	}
	for _, test := range tests {
		got, err := ParseMessage(test.record, test.inStream)
		if err != nil {
			t.Errorf("%s: %v", test.name, err)
			continue
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s = %+v, want %+v", test.name, got, test.want)
		}
		if got.Type() != test.record[0] {
			t.Errorf("%s: Type() = %q, want %q", test.name, got.Type(), test.record[0])
		}
	}
}

// What is cut short, or unknown, is refused, never half parsed.
func TestParseMessageRefusesMalformed(t *testing.T) {
	for _, record := range []built{
		nil,
		message('B').u64(1),
		message('Z'),
		message('D').u32(1).u8('N').raw(tuple()),
		message('I').u32(1).u8('N').u16(1).u8('x'),
		message('T').u32(1000).u8(0).u32(1),
		message('M').u8(1).u64(1).str("p").u32(99).raw([]byte("short")),
	} {
		if _, err := ParseMessage(record, false); !errors.Is(err, ErrMalformed) {
			t.Errorf("%q = %v, want ErrMalformed", []byte(record), err)
		}
	}
}

// Commands are written as the walsender's grammar reads them, a slot name
// checked first since it goes in bare.
func TestReplicationCommands(t *testing.T) {
	commands := []struct {
		got  func() (string, error)
		want string
	}{
		{func() (string, error) { return createSlotCommand("s1", "pgoutput", SlotOptions{}) }, "CREATE_REPLICATION_SLOT s1 LOGICAL pgoutput"},
		{func() (string, error) {
			return createSlotCommand("s1", "pgoutput", SlotOptions{Temporary: true, Snapshot: "noexport", TwoPhase: true})
		}, "CREATE_REPLICATION_SLOT s1 TEMPORARY LOGICAL pgoutput NOEXPORT_SNAPSHOT TWO_PHASE"},
		{func() (string, error) {
			return createSlotCommand("p1", "", SlotOptions{Physical: true, ReserveWAL: true})
		}, "CREATE_REPLICATION_SLOT p1 PHYSICAL RESERVE_WAL"},
		{func() (string, error) {
			return startCommand("s1", 0, StartOptions{PluginArgs: PgoutputOptions{Publications: []string{"a", "b"}, Messages: true}.Args()})
		}, "START_REPLICATION SLOT s1 LOGICAL 0/0 (proto_version '1', publication_names 'a,b', messages 'true')"},
		{func() (string, error) { return startCommand("", 0x16B3748, StartOptions{Physical: true, Timeline: 2}) }, "START_REPLICATION PHYSICAL 0/16B3748 TIMELINE 2"},
	}
	for _, c := range commands {
		if got, err := c.got(); err != nil || got != c.want {
			t.Errorf("%q, %v, want %q", got, err, c.want)
		}
	}
	for _, bad := range []string{"", "Upper", "has-dash", "x; DROP", strings.Repeat("a", 64)} {
		if _, err := createSlotCommand(bad, "pgoutput", SlotOptions{}); !errors.Is(err, ErrSlotName) {
			t.Errorf("slot %q = %v, want ErrSlotName", bad, err)
		}
		if _, err := startCommand(bad, 0, StartOptions{}); !errors.Is(err, ErrSlotName) {
			t.Errorf("start %q = %v, want ErrSlotName", bad, err)
		}
	}
	if _, err := createSlotCommand("s1", "pgoutput", SlotOptions{Snapshot: "later"}); err == nil {
		t.Error("an unknown snapshot action was accepted")
	}
	if got := QuoteLiteral(`it's \ here`); got != `'it''s \ here'` {
		t.Errorf("QuoteLiteral = %s", got)
	}
	args := PgoutputOptions{ProtoVersion: 4, Publications: []string{"p"}, Binary: true, Streaming: "parallel", TwoPhase: true, Origin: "none"}.Args()
	if strings.Join(args, ", ") != "proto_version '4', publication_names 'p', binary 'true', streaming 'parallel', two_phase 'true', origin 'none'" {
		t.Errorf("Args = %q", args)
	}
}

// Each position goes where the server reads it: a flush position sent as the
// apply position would let the server think WAL it still needs was applied.
func TestAppendStandbyStatusUpdate(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := AppendStandbyStatusUpdate(nil, StandbyStatusUpdate{WALWritePosition: 3, WALFlushPosition: 2, WALApplyPosition: 1, ClientTime: at, ReplyRequested: true})
	want := wire.AppendCopyData(nil, AppendStandbyStatus(nil, 3, 2, 1, at, true))
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x\nwant % x", got, want)
	}
}
