package consistency

import (
	"strings"
	"testing"
)

func TestNewQuorumConfig_Valid(t *testing.T) {
	q, err := NewQuorumConfig(3, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if q.N != 3 || q.R != 2 || q.W != 2 {
		t.Errorf("got N=%d R=%d W=%d", q.N, q.R, q.W)
	}
}

func TestNewQuorumConfig_Invalid(t *testing.T) {
	tests := []struct {
		n, r, w int
		want    string
	}{
		{0, 1, 1, "N must be"},
		{3, 0, 2, "R must be"},
		{3, 2, 0, "W must be"},
		{3, 4, 2, "R must be between"},
		{3, 2, 4, "W must be between"},
	}
	for _, tt := range tests {
		_, err := NewQuorumConfig(tt.n, tt.r, tt.w)
		if err == nil {
			t.Errorf("NewQuorumConfig(%d,%d,%d): expected error containing %q", tt.n, tt.r, tt.w, tt.want)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("NewQuorumConfig(%d,%d,%d): error %q does not contain %q", tt.n, tt.r, tt.w, err.Error(), tt.want)
		}
	}
}

func TestQuorumConfig_IsStrong(t *testing.T) {
	q, _ := NewQuorumConfig(3, 2, 2)
	if !q.IsStrong() {
		t.Error("N=3 R=2 W=2: expected IsStrong true (R+W>N)")
	}
	q2, _ := NewQuorumConfig(3, 1, 1)
	if q2.IsStrong() {
		t.Error("N=3 R=1 W=1: expected IsStrong false (R+W<=N)")
	}
}

func TestQuorumConfig_String(t *testing.T) {
	q, _ := NewQuorumConfig(3, 2, 2)
	s := q.String()
	if !strings.Contains(s, "strong") {
		t.Errorf("String (strong): got %q", s)
	}
	if !strings.Contains(s, "N=3") || !strings.Contains(s, "R=2") || !strings.Contains(s, "W=2") {
		t.Errorf("String: got %q", s)
	}
	q2, _ := NewQuorumConfig(3, 1, 1)
	s2 := q2.String()
	if !strings.Contains(s2, "eventual") {
		t.Errorf("String (eventual): got %q", s2)
	}
}
