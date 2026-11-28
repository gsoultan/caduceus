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
//
// A COPY has one format for all its columns, so a BinaryParam can end up in
// a text COPY when another column of the same row has no binary form. There
// it is written as its text: MarshalText's, if it is an
// encoding.TextMarshaler too, or else the codec's own reading of its binary
// form, which only types the codec decodes have.
type BinaryParam interface {
	AppendBinaryParam(dst []byte, oid uint32) (out []byte, null bool, err error)
}
