// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// namedRewrite turns the @name placeholders of one statement into positional
// parameters, and the named arguments into the positional arguments to match.
type namedRewrite struct {
	sql    string
	values map[string]any
	strict bool

	out  strings.Builder
	args []any
	// positions is the parameter number each name was given, the first time
	// it appeared.
	positions map[string]int
}

// rewriteNamed rewrites sql for values. strict refuses a name values lacks,
// and a value no name uses.
func rewriteNamed(sql string, values map[string]any, strict bool) (string, []any, error) {
	r := &namedRewrite{sql: sql, values: values, strict: strict, positions: make(map[string]int, len(values))}
	r.out.Grow(len(sql))
	for i := 0; i < len(sql); {
		next, err := r.step(i)
		if err != nil {
			return "", nil, err
		}
		i = next
	}
	if err := r.allUsed(); err != nil {
		return "", nil, err
	}
	return r.out.String(), r.args, nil
}

// step copies what starts at i, a placeholder rewritten, and returns where
// the next thing starts.
func (r *namedRewrite) step(i int) (int, error) {
	if end := literalEnd(r.sql, i); end > i {
		r.out.WriteString(r.sql[i:end])
		return end, nil
	}
	if r.sql[i] == '@' && i+1 < len(r.sql) && nameStart(r.sql[i+1]) {
		return r.parameter(i)
	}
	r.out.WriteByte(r.sql[i])
	return i + 1, nil
}

// parameter rewrites the placeholder at i.
func (r *namedRewrite) parameter(i int) (int, error) {
	end := i + 1
	for end < len(r.sql) && nameChar(r.sql[end]) {
		end++
	}
	position, err := r.position(r.sql[i+1 : end])
	if err != nil {
		return 0, err
	}
	r.out.WriteByte('$')
	r.out.WriteString(strconv.Itoa(position))
	return end, nil
}

// position returns the parameter number of name, giving it the next one the
// first time it appears.
func (r *namedRewrite) position(name string) (int, error) {
	if position, ok := r.positions[name]; ok {
		return position, nil
	}
	value, ok := r.values[name]
	if !ok && r.strict {
		return 0, fmt.Errorf("%w: the statement names @%s, which the arguments lack", ErrNamedArgs, name)
	}
	r.args = append(r.args, value)
	r.positions[name] = len(r.args)
	return len(r.args), nil
}

// allUsed refuses, when strict, a value that no placeholder named.
func (r *namedRewrite) allUsed() error {
	if !r.strict || len(r.positions) == len(r.values) {
		return nil
	}
	var unused []string
	for name := range r.values {
		if _, ok := r.positions[name]; !ok {
			unused = append(unused, name)
		}
	}
	slices.Sort(unused)
	return fmt.Errorf("%w: the statement does not name %s", ErrNamedArgs, strings.Join(unused, ", "))
}
