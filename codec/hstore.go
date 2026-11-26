// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import (
	"errors"
	"slices"
	"strings"
)

var errHstore = errors.New("caduceus/codec: malformed hstore")

// Hstore is a value of the hstore extension's type: keys to values, a nil
// value for NULL. A nil Hstore is NULL. hstore's type OID differs from one
// database to the next, so it is read and written as text, whatever the
// column's OID.
type Hstore map[string]*string

func (h *Hstore) scanColumn(oid uint32, format int16, src []byte) error {
	if src == nil {
		*h = nil
		return nil
	}
	text, err := asString(oid, format, src)
	if err != nil {
		return err
	}
	parsed, err := parseHstore(text)
	if err != nil {
		return err
	}
	*h = parsed
	return nil
}

func (h Hstore) encodeColumn(dst []byte, _ uint32) ([]byte, bool, error) {
	if h == nil {
		return dst, true, nil
	}
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for i, key := range keys {
		if i > 0 {
			dst = append(dst, ", "...)
		}
		dst = append(appendQuoted(dst, []byte(key)), "=>"...)
		if value := h[key]; value != nil {
			dst = appendQuoted(dst, []byte(*value))
			continue
		}
		dst = append(dst, "NULL"...)
	}
	return dst, false, nil
}

// parseHstore reads "key"=>"value" pairs separated by commas, as hstore_out
// prints them, a value NULL unquoted for NULL.
func parseHstore(text string) (Hstore, error) {
	h := Hstore{}
	for text = strings.TrimSpace(text); text != ""; text = strings.TrimSpace(text) {
		key, rest, ok := hstoreString(text)
		if !ok || key == nil {
			return nil, errHstore
		}
		rest, found := strings.CutPrefix(strings.TrimSpace(rest), "=>")
		if !found {
			return nil, errHstore
		}
		value, rest, ok := hstoreString(strings.TrimSpace(rest))
		if !ok {
			return nil, errHstore
		}
		h[*key] = value
		text = strings.TrimSpace(rest)
		if text != "" && !strings.HasPrefix(text, ",") {
			return nil, errHstore
		}
		text = strings.TrimPrefix(text, ",")
	}
	return h, nil
}

// hstoreString reads a double-quoted string with backslash escapes, or NULL,
// which is nil.
func hstoreString(text string) (*string, string, bool) {
	if rest, ok := strings.CutPrefix(text, "NULL"); ok {
		return nil, rest, true
	}
	if !strings.HasPrefix(text, `"`) {
		return nil, "", false
	}
	var value strings.Builder
	for i := 1; i < len(text); i++ {
		switch c := text[i]; {
		case c == '\\' && i+1 < len(text):
			i++
			value.WriteByte(text[i])
		case c == '"':
			s := value.String()
			return &s, text[i+1:], true
		default:
			value.WriteByte(c)
		}
	}
	return nil, "", false
}
