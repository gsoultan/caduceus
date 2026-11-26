// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"strconv"
	"strings"
)

// CommandTag is what the server says a statement did, such as "INSERT 0 3",
// "UPDATE 2" or "CREATE TABLE".
type CommandTag string

// String returns the tag as the server sent it.
func (t CommandTag) String() string { return string(t) }

// RowsAffected returns the number of rows the statement inserted, updated,
// deleted, selected, copied, moved or fetched: the integer the tag ends with.
// A tag that ends with none, as a CREATE TABLE's does, or with one too large
// for an int64, has 0.
func (t CommandTag) RowsAffected() int64 {
	last := string(t[strings.LastIndexByte(string(t), ' ')+1:])
	n, err := strconv.ParseUint(last, 10, 63)
	if err != nil {
		return 0
	}
	return int64(n)
}

// Insert reports whether the tag is an INSERT's.
func (t CommandTag) Insert() bool { return strings.HasPrefix(string(t), "INSERT") }

// Update reports whether the tag is an UPDATE's.
func (t CommandTag) Update() bool { return strings.HasPrefix(string(t), "UPDATE") }

// Delete reports whether the tag is a DELETE's.
func (t CommandTag) Delete() bool { return strings.HasPrefix(string(t), "DELETE") }

// Select reports whether the tag is a SELECT's.
func (t CommandTag) Select() bool { return strings.HasPrefix(string(t), "SELECT") }
