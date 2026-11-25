// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

func relationMessage(id uint32, namespace, name string, columns ...string) []byte {
	out := binary.BigEndian.AppendUint32([]byte{msgRelation}, id)
	out = append(append(out, namespace...), 0)
	out = append(append(out, name...), 0)
	out = append(out, 'd')
	out = binary.BigEndian.AppendUint16(out, uint16(len(columns)))
	for _, column := range columns {
		out = append(out, 0)
		out = append(append(out, column...), 0)
		out = binary.BigEndian.AppendUint32(out, 25)
		out = binary.BigEndian.AppendUint32(out, 0xFFFFFFFF)
	}
	return out
}

func beginMessage(final uint64, at time.Time, xid uint32) []byte {
	out := binary.BigEndian.AppendUint64([]byte{msgBegin}, final)
	out = binary.BigEndian.AppendUint64(out, uint64(at.Sub(postgresEpoch).Microseconds()))
	return binary.BigEndian.AppendUint32(out, xid)
}

func tuple(columns ...[]byte) []byte {
	out := binary.BigEndian.AppendUint16(nil, uint16(len(columns)))
	for _, column := range columns {
		out = append(out, column...)
	}
	return out
}

func textColumn(value string) []byte {
	out := binary.BigEndian.AppendUint32([]byte{tupleText}, uint32(len(value)))
	return append(out, value...)
}

var (
	nullColumn      = []byte{tupleNull}
	unchangedColumn = []byte{tupleUnchanged}
)

func changeMessage(op Op, relation uint32, parts ...[]byte) []byte {
	out := binary.BigEndian.AppendUint32([]byte{byte(op)}, relation)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func decoderWithUsers(t *testing.T) *Decoder {
	t.Helper()
	d := NewDecoder()
	if _, ok, err := d.Decode(relationMessage(7, "public", "users", "id", "email", "bio")); ok || err != nil {
		t.Fatalf("Decode(Relation) = %v, %v", ok, err)
	}
	return d
}

// A whole transaction as the server sends one: Begin, Relation, then changes
// that must be named by the Relation and stamped by the Begin.
func TestDecoderFollowsATransaction(t *testing.T) {
	t.Parallel()
	d := decoderWithUsers(t)
	at := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	if _, ok, err := d.Decode(beginMessage(0xABCDEF, at, 777)); ok || err != nil {
		t.Fatalf("Decode(Begin) = %v, %v", ok, err)
	}
	change, ok, err := d.Decode(changeMessage(Insert, 7, []byte{tupleNew}, tuple(textColumn("1"), textColumn("a@b"), nullColumn)))
	if !ok || err != nil {
		t.Fatalf("Decode(Insert) = %v, %v", ok, err)
	}
	if change.Op != Insert || change.Relation.Name != "users" || change.Relation.Namespace != "public" {
		t.Fatalf("change = %+v", change)
	}
	want := Transaction{FinalLSN: 0xABCDEF, Committed: at, XID: 777}
	if change.Transaction != want {
		t.Fatalf("Transaction = %+v, want %+v", change.Transaction, want)
	}
	if change.Before != nil || change.After["id"] != "1" || change.After["email"] != "a@b" {
		t.Fatalf("After = %v", change.After)
	}
	if v, present := change.After["bio"]; !present || v != nil {
		t.Fatalf("a NULL column must be present as nil, got %v, %v", v, present)
	}
}

// An unchanged TOASTed value used to be recorded as nil, which a consumer
// replaying the change could not tell from a real NULL and so blanked the
// column. It must be absent instead.
func TestUnchangedToastIsAbsentNotNull(t *testing.T) {
	t.Parallel()
	d := decoderWithUsers(t)
	change, ok, err := d.Decode(changeMessage(Update, 7, []byte{tupleNew}, tuple(textColumn("1"), textColumn("x"), unchangedColumn)))
	if !ok || err != nil {
		t.Fatalf("Decode = %v, %v", ok, err)
	}
	if _, present := change.After["bio"]; present {
		t.Fatalf("unchanged TOAST column present: %v", change.After)
	}
	if change.Before != nil {
		t.Fatalf("REPLICA IDENTITY DEFAULT sends no old tuple, got Before = %v", change.Before)
	}
}

func TestDecodeUpdateAndDeleteCarryTheOldRow(t *testing.T) {
	t.Parallel()
	d := decoderWithUsers(t)
	update, _, err := d.Decode(changeMessage(Update, 7,
		[]byte{tupleKey}, tuple(textColumn("1"), nullColumn, nullColumn),
		[]byte{tupleNew}, tuple(textColumn("2"), textColumn("x"), nullColumn)))
	if err != nil || update.Before["id"] != "1" || update.After["id"] != "2" {
		t.Fatalf("update = %+v, %v", update, err)
	}
	del, _, err := d.Decode(changeMessage(Delete, 7, []byte{tupleOld}, tuple(textColumn("2"), textColumn("x"), nullColumn)))
	if err != nil || del.Before["email"] != "x" || del.After != nil {
		t.Fatalf("delete = %+v, %v", del, err)
	}
}

// Maps used to be recycled through a sync.Pool, which cleared them under any
// consumer that kept a change past its loop iteration.
func TestChangesDoNotShareMaps(t *testing.T) {
	t.Parallel()
	d := decoderWithUsers(t)
	msg := changeMessage(Insert, 7, []byte{tupleNew}, tuple(textColumn("1"), nullColumn, nullColumn))
	first, _, _ := d.Decode(msg)
	second, _, _ := d.Decode(msg)
	second.After["id"] = "changed"
	if first.After["id"] != "1" {
		t.Fatal("two changes share one map")
	}
}

func TestDecodeSkipsAnUnknownRelation(t *testing.T) {
	t.Parallel()
	d := NewDecoder()
	_, ok, err := d.Decode(changeMessage(Insert, 99, []byte{tupleNew}, tuple(textColumn("1"))))
	if ok || err != nil {
		t.Fatalf("Decode = %v, %v", ok, err)
	}
}

func TestDecodeRejectsMalformedMessages(t *testing.T) {
	t.Parallel()
	d := decoderWithUsers(t)
	for name, msg := range map[string][]byte{
		"empty":              nil,
		"short relation id":  {byte(Insert), 0, 0},
		"truncated tuple":    changeMessage(Insert, 7, []byte{tupleNew}, []byte{0, 3, tupleText, 0, 0, 0, 9, 'a'}),
		"delete without old": changeMessage(Delete, 7, []byte{tupleNew}, tuple(textColumn("1"))),
		"bad column kind":    changeMessage(Insert, 7, []byte{tupleNew}, tuple([]byte{'?'})),
		"truncated begin":    {msgBegin, 0, 1},
	} {
		if _, _, err := d.Decode(msg); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: err = %v, want ErrMalformed", name, err)
		}
	}
}

func TestLSNRoundTrips(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		lsn  LSN
		text string
	}{{0, "0/0"}, {0x16B3748, "0/16B3748"}, {0x1_0000_0000, "1/0"}, {^LSN(0), "FFFFFFFF/FFFFFFFF"}} {
		if got := tt.lsn.String(); got != tt.text {
			t.Errorf("%#x.String() = %q, want %q", uint64(tt.lsn), got, tt.text)
		}
		if got, err := ParseLSN(tt.text); err != nil || got != tt.lsn {
			t.Errorf("ParseLSN(%q) = %#x, %v", tt.text, uint64(got), err)
		}
	}
	for _, bad := range []string{"", "16B3748", "x/1", "1/100000000", "1/2/3"} {
		if _, err := ParseLSN(bad); !errors.Is(err, ErrLSNFormat) {
			t.Errorf("ParseLSN(%q) = %v, want ErrLSNFormat", bad, err)
		}
	}
}

func TestStreamMessages(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 6000, time.UTC)
	micros := uint64(now.Sub(postgresEpoch).Microseconds())

	xlog := binary.BigEndian.AppendUint64([]byte{XLogData}, 100)
	xlog = binary.BigEndian.AppendUint64(xlog, 200)
	xlog = binary.BigEndian.AppendUint64(xlog, micros)
	xlog = append(xlog, "BODY"...)
	record, err := ParseXLogData(xlog)
	if err != nil || record.Start != 100 || record.ServerEnd != 200 || !record.ServerTime.Equal(now) || string(record.Data) != "BODY" || record.End() != 104 {
		t.Fatalf("ParseXLogData = %+v, %v", record, err)
	}
	if _, err := ParseXLogData(xlog[:25]); !errors.Is(err, ErrMalformed) {
		t.Fatalf("ParseXLogData without a record = %v", err)
	}

	keep := binary.BigEndian.AppendUint64([]byte{PrimaryKeepalive}, 300)
	keep = binary.BigEndian.AppendUint64(keep, micros)
	keep = append(keep, 1)
	k, err := ParseKeepalive(keep)
	if err != nil || k.ServerEnd != 300 || !k.ReplyRequested {
		t.Fatalf("ParseKeepalive = %+v, %v", k, err)
	}
	if _, err := ParseKeepalive(keep[:5]); !errors.Is(err, ErrMalformed) {
		t.Fatalf("short keepalive = %v", err)
	}

	status := AppendStandbyStatus(nil, 3, 2, 1, now, true)
	if len(status) != 34 || status[0] != StandbyStatus || binary.BigEndian.Uint64(status[1:]) != 3 ||
		binary.BigEndian.Uint64(status[9:]) != 2 || binary.BigEndian.Uint64(status[17:]) != 1 ||
		binary.BigEndian.Uint64(status[25:]) != micros || status[33] != 1 {
		t.Fatalf("AppendStandbyStatus = %x", status)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(beginMessage(1, time.Now(), 1))
	f.Add(relationMessage(7, "public", "users", "id", "email"))
	f.Add(changeMessage(Update, 7, []byte{tupleKey}, tuple(textColumn("1"), unchangedColumn)))
	f.Fuzz(func(t *testing.T, record []byte) {
		d := NewDecoder()
		d.Decode(relationMessage(7, "public", "users", "id", "email", "bio"))
		_, _, _ = d.Decode(record)
	})
}

func BenchmarkDecodeUpdate(b *testing.B) {
	d := NewDecoder()
	d.Decode(relationMessage(7, "public", "users", "id", "email", "bio"))
	msg := changeMessage(Update, 7, []byte{tupleNew}, tuple(textColumn("42"), textColumn("someone@example.com"), unchangedColumn))
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := d.Decode(msg); err != nil {
			b.Fatal(err)
		}
	}
}
