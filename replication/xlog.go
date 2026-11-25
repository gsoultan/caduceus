// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "time"

// WALRecord is an XLogData message: one WAL record and where it sits.
type WALRecord struct {
	// Start is where the record begins in the WAL.
	Start LSN
	// ServerEnd is the end of the WAL on the server when it was sent.
	ServerEnd LSN
	// ServerTime is the server's clock when it was sent.
	ServerTime time.Time
	// Data is the record itself, aliasing the message it was parsed from.
	Data []byte
}

// End is the position just past the record, the one to confirm once it is
// handled.
func (w WALRecord) End() LSN { return w.Start + LSN(len(w.Data)) }

// ParseXLogData parses a CopyData body whose first byte is XLogData.
func ParseXLogData(body []byte) (WALRecord, error) {
	if len(body) == 0 || body[0] != XLogData {
		return WALRecord{}, ErrMalformed
	}
	r := reader{data: body[1:]}
	record := WALRecord{Start: LSN(r.uint64()), ServerEnd: LSN(r.uint64()), ServerTime: r.timestamp()}
	if r.err != nil || len(r.data) == 0 {
		return WALRecord{}, ErrMalformed
	}
	record.Data = r.data
	return record, nil
}
