// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import (
	"strconv"
	"strings"
)

// LSN is a position in the write-ahead log.
type LSN uint64

// String renders the LSN in PostgreSQL's own "X/Y" notation, two hexadecimal
// halves, upper case and unpadded: the same text psql, pg_stat_replication and
// pg_lsn use, so a recorded position can be compared against the server by hand.
func (l LSN) String() string {
	var buf [17]byte
	return string(l.AppendText(buf[:0]))
}

// AppendText appends the LSN's "X/Y" text to dst.
func (l LSN) AppendText(dst []byte) []byte {
	dst = appendHex(dst, uint64(l)>>32)
	dst = append(dst, '/')
	return appendHex(dst, uint64(l)&0xFFFFFFFF)
}

func appendHex(dst []byte, v uint64) []byte {
	const digits = "0123456789ABCDEF"
	if v == 0 {
		return append(dst, '0')
	}
	var tmp [8]byte
	i := len(tmp)
	for ; v > 0; v >>= 4 {
		i--
		tmp[i] = digits[v&0xF]
	}
	return append(dst, tmp[i:]...)
}

// ParseLSN reads an LSN from its "X/Y" text.
func ParseLSN(s string) (LSN, error) {
	high, low, ok := strings.Cut(s, "/")
	if !ok {
		return 0, ErrLSNFormat
	}
	hi, err := strconv.ParseUint(high, 16, 32)
	if err != nil {
		return 0, ErrLSNFormat
	}
	lo, err := strconv.ParseUint(low, 16, 32)
	if err != nil {
		return 0, ErrLSNFormat
	}
	return LSN(hi<<32 | lo), nil
}
