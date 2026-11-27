// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import "time"

// encodePointer encodes a pointer to one of the common basic types without
// reflection, and reports false for any other argument. A caller that binds
// its arguments from fields of its own, as an ORM's statement arenas do, hands
// over a pointer for every one, and reflecting each into an interface would
// cost an allocation the value itself does not.
func encodePointer(dst []byte, oid uint32, arg any) (out []byte, format int16, null, ok bool, err error) {
	switch v := arg.(type) {
	case *string:
		if v == nil {
			return dst, 0, true, true, nil
		}
		return append(dst, *v...), 0, false, true, nil
	case *int64:
		if v == nil {
			return dst, 0, true, true, nil
		}
		out, format, null, err = encodeInt(dst, oid, *v, arg)
	case *int32:
		if v == nil {
			return dst, 0, true, true, nil
		}
		out, format, null, err = encodeInt(dst, oid, int64(*v), arg)
	case *int16:
		if v == nil {
			return dst, 0, true, true, nil
		}
		out, format, null, err = encodeInt(dst, oid, int64(*v), arg)
	case *int:
		if v == nil {
			return dst, 0, true, true, nil
		}
		out, format, null, err = encodeInt(dst, oid, int64(*v), arg)
	case *float64:
		if v == nil {
			return dst, 0, true, true, nil
		}
		out, format, null, err = encodeFloat(dst, oid, *v)
	case *bool:
		if v == nil {
			return dst, 0, true, true, nil
		}
		out, format, null, err = encodeBool(dst, oid, *v)
	case *time.Time:
		if v == nil {
			return dst, 0, true, true, nil
		}
		out, format, null, err = encodeTime(dst, oid, *v)
	case *[16]byte:
		if v == nil {
			return dst, 0, true, true, nil
		}
		if oid == UUID {
			return append(dst, v[:]...), 1, false, true, nil
		}
		out, format, null, err = encodeText(dst, formatUUID(v[:]))
	case *[]byte:
		if v == nil {
			return dst, 0, true, true, nil
		}
		out, format, null, err = encodeBytes(dst, oid, *v)
	default:
		return dst, 0, false, false, nil
	}
	return out, format, null, true, err
}
