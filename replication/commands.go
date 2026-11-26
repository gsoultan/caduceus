// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// The replication protocol's commands, for a connection made with
// replication=database (logical) or replication=true (physical) among its
// runtime parameters. They go over the simple protocol, as the walsender
// requires, so a slot's name, which the grammar takes bare, is checked first.

// ErrSlotName is a slot name PostgreSQL would refuse, or that could not go
// into a command bare: 1 to 63 of a-z, 0-9 and _.
var ErrSlotName = errors.New("caduceus/replication: a slot name is 1 to 63 characters of a-z, 0-9 and _")

var slotName = regexp.MustCompile(`^[a-z0-9_]{1,63}$`)

// ValidSlotName checks a slot name, which the replication grammar takes bare.
func ValidSlotName(name string) error {
	if !slotName.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrSlotName, name)
	}
	return nil
}

// QuoteLiteral quotes s as a string of the replication command grammar,
// doubling each quote. It is not caduceus.QuoteLiteral: that writes a string
// with a backslash as E'...', which SQL reads and the walsender's grammar
// refuses with a syntax error, since it reads a backslash as itself.
func QuoteLiteral(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}

// IdentifySystem runs IDENTIFY_SYSTEM.
func IdentifySystem(ctx context.Context, conn *wire.Conn) (SystemIdentity, error) {
	row, err := oneRow(ctx, conn, "IDENTIFY_SYSTEM", 4)
	if err != nil {
		return SystemIdentity{}, err
	}
	timeline, err := strconv.ParseInt(string(row[1]), 10, 32)
	if err != nil {
		return SystemIdentity{}, ErrMalformed
	}
	position, err := ParseLSN(string(row[2]))
	if err != nil {
		return SystemIdentity{}, err
	}
	return SystemIdentity{SystemID: string(row[0]), Timeline: int32(timeline), XLogPos: position, Database: string(row[3])}, nil
}

// CreateReplicationSlot creates a slot: a logical one decoded by plugin,
// such as pgoutput, or a physical one with options.Physical. A slot holds WAL
// until it is dropped, which a slot nobody reads from fills the primary's
// disk with; a Temporary one goes with the connection.
func CreateReplicationSlot(ctx context.Context, conn *wire.Conn, name, plugin string, options SlotOptions) (Slot, error) {
	command, err := createSlotCommand(name, plugin, options)
	if err != nil {
		return Slot{}, err
	}
	row, err := oneRow(ctx, conn, command, 4)
	if err != nil {
		return Slot{}, err
	}
	slot := Slot{Name: string(row[0]), SnapshotName: string(row[2]), Plugin: string(row[3])}
	if row[1] != nil {
		if slot.ConsistentPoint, err = ParseLSN(string(row[1])); err != nil {
			return Slot{}, err
		}
	}
	return slot, nil
}

// createSlotCommand writes CREATE_REPLICATION_SLOT in the grammar every
// server from PostgreSQL 10 on reads.
func createSlotCommand(name, plugin string, options SlotOptions) (string, error) {
	if err := ValidSlotName(name); err != nil {
		return "", err
	}
	command := "CREATE_REPLICATION_SLOT " + name
	if options.Temporary {
		command += " TEMPORARY"
	}
	if options.Physical {
		if options.ReserveWAL {
			return command + " PHYSICAL RESERVE_WAL", nil
		}
		return command + " PHYSICAL", nil
	}
	if !slotName.MatchString(plugin) {
		return "", fmt.Errorf("caduceus/replication: plugin %q is not a plain name", plugin)
	}
	command += " LOGICAL " + plugin
	switch options.Snapshot {
	case "", "export":
	case "noexport":
		command += " NOEXPORT_SNAPSHOT"
	case "use":
		command += " USE_SNAPSHOT"
	default:
		return "", fmt.Errorf("caduceus/replication: snapshot %q is not one of export, noexport, use", options.Snapshot)
	}
	if options.TwoPhase {
		command += " TWO_PHASE"
	}
	return command, nil
}

// DropReplicationSlot drops a slot, and the WAL position it holds with it,
// which a consumer that later starts from a new slot of the same name has
// lost. With wait, a slot in use by another connection is waited for rather
// than refused.
func DropReplicationSlot(ctx context.Context, conn *wire.Conn, name string, wait bool) error {
	if err := ValidSlotName(name); err != nil {
		return err
	}
	command := "DROP_REPLICATION_SLOT " + name
	if wait {
		command += " WAIT"
	}
	_, err := conn.Exec(ctx, command)
	return err
}

// ReadReplicationSlot runs READ_REPLICATION_SLOT, PostgreSQL 15 and later.
func ReadReplicationSlot(ctx context.Context, conn *wire.Conn, name string) (SlotState, error) {
	if err := ValidSlotName(name); err != nil {
		return SlotState{}, err
	}
	row, err := oneRow(ctx, conn, "READ_REPLICATION_SLOT "+name, 3)
	if err != nil || row[0] == nil {
		return SlotState{}, err
	}
	state := SlotState{Exists: true, Type: string(row[0])}
	if row[1] != nil {
		if state.RestartLSN, err = ParseLSN(string(row[1])); err != nil {
			return SlotState{}, err
		}
	}
	if row[2] != nil {
		timeline, err := strconv.ParseInt(string(row[2]), 10, 32)
		if err != nil {
			return SlotState{}, ErrMalformed
		}
		state.RestartTimeline = int32(timeline)
	}
	return state, nil
}

// TimelineHistoryFile runs TIMELINE_HISTORY for timeline.
func TimelineHistoryFile(ctx context.Context, conn *wire.Conn, timeline int32) (TimelineHistory, error) {
	row, err := oneRow(ctx, conn, "TIMELINE_HISTORY "+strconv.Itoa(int(timeline)), 2)
	if err != nil {
		return TimelineHistory{}, err
	}
	return TimelineHistory{FileName: string(row[0]), Content: row[1]}, nil
}

// StartReplication starts streaming from slot at start, and returns once the
// connection is streaming: read it with Receive, ParseXLogData and
// ParseKeepalive, and answer with SendStandbyStatusUpdate. A logical slot
// started at 0 resumes from its confirmed_flush_lsn, which is what keeps
// what it held while nobody read; IDENTIFY_SYSTEM's position would skip it.
// A physical stream may have no slot.
func StartReplication(ctx context.Context, conn *wire.Conn, slot string, start LSN, options StartOptions) error {
	command, err := startCommand(slot, start, options)
	if err != nil {
		return err
	}
	return conn.StartCopyBoth(ctx, command)
}

// startCommand writes START_REPLICATION.
func startCommand(slot string, start LSN, options StartOptions) (string, error) {
	command := "START_REPLICATION"
	if slot != "" || !options.Physical {
		if err := ValidSlotName(slot); err != nil {
			return "", err
		}
		command += " SLOT " + slot
	}
	if options.Physical {
		command += " PHYSICAL " + start.String()
		if options.Timeline > 0 {
			command += " TIMELINE " + strconv.Itoa(int(options.Timeline))
		}
		return command, nil
	}
	command += " LOGICAL " + start.String()
	if len(options.PluginArgs) > 0 {
		command += " (" + strings.Join(options.PluginArgs, ", ") + ")"
	}
	return command, nil
}

// SendStandbyStatusUpdate sends a Standby Status Update on a streaming
// connection. Only the write is bounded by ctx's deadline: the connection's
// reads, which another part of the program may be blocked in, are left
// alone.
func SendStandbyStatusUpdate(ctx context.Context, conn *wire.Conn, update StandbyStatusUpdate) error {
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.NetConn().SetWriteDeadline(deadline); err != nil {
			return err
		}
		defer conn.NetConn().SetWriteDeadline(time.Time{})
	}
	return conn.Send(AppendStandbyStatusUpdate(conn.Buffer(), update))
}

// AppendStandbyStatusUpdate appends a Standby Status Update in the CopyData
// that carries it.
func AppendStandbyStatusUpdate(dst []byte, update StandbyStatusUpdate) []byte {
	now := update.ClientTime
	if now.IsZero() {
		now = time.Now()
	}
	var body [34]byte
	status := AppendStandbyStatus(body[:0], update.WALWritePosition, update.WALFlushPosition, update.WALApplyPosition, now, update.ReplyRequested)
	return wire.AppendCopyData(dst, status)
}

// oneRow runs a command that answers with one row of columns columns.
func oneRow(ctx context.Context, conn *wire.Conn, command string, columns int) ([][]byte, error) {
	result, err := conn.Exec(ctx, command)
	if err != nil {
		return nil, err
	}
	if len(result.Rows) != 1 || len(result.Rows[0]) < columns {
		return nil, fmt.Errorf("%w: %s answered %d rows", ErrMalformed, strings.Fields(command)[0], len(result.Rows))
	}
	return result.Rows[0], nil
}
