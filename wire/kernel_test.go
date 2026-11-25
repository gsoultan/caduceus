// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import "testing"

func TestKernelAtLeast(t *testing.T) {
	cases := []struct {
		release string
		want    bool
	}{
		{"6.18.44-fc-v880", true},
		{"4.18.0-553.el8", true},
		{"5.4.0", true},
		{"4.19", true},
		{"4.17.19", false},
		{"4.4.0", false}, // what gVisor reports
		{"3.10.0-1160.el7.x86_64", false},
		{"10.0.1", true},
		{"", false},
		{"4", false},
		{"four.eighteen", false},
		{"4.x", false},
	}
	for _, c := range cases {
		if got := kernelAtLeast(c.release, 4, 18); got != c.want {
			t.Errorf("kernelAtLeast(%q, 4, 18) = %v, want %v", c.release, got, c.want)
		}
	}
}
