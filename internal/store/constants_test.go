package store

import "testing"

func TestBoolToString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   bool
		want string
	}{
		{name: "true", in: true, want: "true"},
		{name: "false", in: false, want: "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := boolToString(tt.in); got != tt.want {
				t.Fatalf("boolToString(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
