// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "testing"

// TestCommandTag proves the tag is read the way pgx reads it: the count is the
// last space-separated integer, whatever comes before it, and a tag without
// one, or with one too large or not a plain decimal, counts no rows rather
// than a wrong number.
func TestCommandTag(t *testing.T) {
	tests := []struct {
		tag                             CommandTag
		rows                            int64
		insert, update, delete, select_ bool
	}{
		{"INSERT 0 3", 3, true, false, false, false},
		{"INSERT 0 1", 1, true, false, false, false},
		{"UPDATE 12", 12, false, true, false, false},
		{"DELETE 0", 0, false, false, true, false},
		{"SELECT 7", 7, false, false, false, true},
		{"MERGE 4", 4, false, false, false, false},
		{"COPY 1000000", 1000000, false, false, false, false},
		{"FETCH 5", 5, false, false, false, false},
		{"MOVE 2", 2, false, false, false, false},
		{"CREATE TABLE", 0, false, false, false, false},
		{"BEGIN", 0, false, false, false, false},
		{"", 0, false, false, false, false},
		{"SELECT ", 0, false, false, false, true},
		{"SELECT +5", 0, false, false, false, true},
		{"SELECT -5", 0, false, false, false, true},
		{"SELECT 1_000", 0, false, false, false, true},
		{"SELECT 99999999999999999999", 0, false, false, false, true},
		{"SELECT 9223372036854775807", 9223372036854775807, false, false, false, true},
		{"SELECT 9223372036854775808", 0, false, false, false, true},
		{"select 3", 3, false, false, false, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.tag), func(t *testing.T) {
			if got := tt.tag.RowsAffected(); got != tt.rows {
				t.Errorf("RowsAffected() = %d, want %d", got, tt.rows)
			}
			got := [4]bool{tt.tag.Insert(), tt.tag.Update(), tt.tag.Delete(), tt.tag.Select()}
			if want := [4]bool{tt.insert, tt.update, tt.delete, tt.select_}; got != want {
				t.Errorf("Insert, Update, Delete, Select = %v, want %v", got, want)
			}
			if tt.tag.String() != string(tt.tag) {
				t.Errorf("String() = %q", tt.tag.String())
			}
		})
	}
}
