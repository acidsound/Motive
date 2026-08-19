package model

import "testing"

func TestNormalizeEffort(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "low", in: "low", want: "low"},
		{name: "medium", in: "medium", want: "medium"},
		{name: "xhigh", in: "xhigh", want: "xhigh"},
		{name: "case insensitive", in: " XHIGH ", want: "xhigh"},
		{name: "unsupported high", in: "high", want: "low"},
		{name: "unsupported max", in: "max", want: "low"},
		{name: "empty", in: "", want: "low"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeEffort(tt.in); got != tt.want {
				t.Fatalf("normalizeEffort(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
