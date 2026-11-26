// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import "errors"

var errBits = errors.New("caduceus/codec: malformed bit string")

// Type OIDs of the bit string types.
const (
	Bit    uint32 = 1560
	Varbit uint32 = 1562
)

// Bits is a value of bit or varbit: Len bits, packed most significant first
// into Bytes. Valid is false for NULL.
type Bits struct {
	Bytes []byte
	Len   int32
	Valid bool
}

func (b *Bits) scanColumn(oid uint32, format int16, src []byte) error {
	*b = Bits{}
	if src == nil {
		return nil
	}
	text, err := asString(oid, format, src)
	if err != nil {
		return err
	}
	bits := Bits{Bytes: make([]byte, (len(text)+7)/8), Len: int32(len(text)), Valid: true}
	for i, c := range []byte(text) {
		switch c {
		case '1':
			bits.Bytes[i/8] |= 0x80 >> (i % 8)
		case '0':
		default:
			return errBits
		}
	}
	*b = bits
	return nil
}

func (b Bits) encodeColumn(dst []byte, _ uint32) ([]byte, bool, error) {
	if !b.Valid {
		return dst, true, nil
	}
	if int(b.Len) > 8*len(b.Bytes) || b.Len < 0 {
		return dst, false, errBits
	}
	for i := range int(b.Len) {
		dst = append(dst, '0'+(b.Bytes[i/8]>>(7-i%8))&1)
	}
	return dst, false, nil
}
