// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import "testing"

// Every backend message body reaches one of these parsers straight off the
// network. Whatever the server, or something pretending to be one, sends, the
// result must be an error and never a panic.
func FuzzParseBackendMessages(f *testing.F) {
	f.Add([]byte{0, 1, 'i', 'd', 0, 0, 0, 0, 1, 0, 1, 0, 0, 0, 23, 0, 4, 0xff, 0xff, 0xff, 0xff, 0, 1})
	f.Add([]byte{0, 2, 0, 0, 0, 1, '7', 0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{'S', 'E', 'R', 'R', 'O', 'R', 0, 'C', '4', '2', 'P', '0', '1', 0, 0})
	f.Fuzz(func(t *testing.T, body []byte) {
		// What parses as a description is the same as itself, and comparing
		// it with something else is no more to be trusted with it.
		if fields, err := ParseRowDescription(nil, body); err == nil && !SameRowDescription(fields, body) {
			t.Errorf("a description of %d columns was not the same as itself", len(fields))
		}
		_ = SameRowDescription([]Field{{Name: "id", DataTypeOID: 23}}, body)
		_, _ = ParseParameterDescription(nil, body)
		_, _ = SplitDataRow(nil, body)
		_ = ParseError(body).Error()
		_, _ = parseNotification(body)
	})
}
