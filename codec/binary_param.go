// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

// BinaryParam is a type that writes its own parameter in the binary format of
// the parameter's type, for a type whose binary form the codec does not know,
// such as a decimal of an application's own. AppendBinaryParam appends the
// value for type oid to dst and reports null for NULL, in which case nothing
// is appended. A nil pointer to such a type is NULL without being asked.
//
// Unlike a Valuer, which hands back a Go value for the codec to encode, it is
// told the parameter's type, and what it writes is sent as binary: the server
// reads it with the type's receive function, so it must be that function's
// input exactly.
type BinaryParam interface {
	AppendBinaryParam(dst []byte, oid uint32) (out []byte, null bool, err error)
}
