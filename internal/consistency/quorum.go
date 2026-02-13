package consistency

import "fmt"

// QuorumConfig holds quorum parameters.
type QuorumConfig struct {
	N int // replication factor
	R int // read quorum
	W int // write quorum
}

// NewQuorumConfig creates a quorum configuration and validates it.
func NewQuorumConfig(n, r, w int) (*QuorumConfig, error) {
	if n < 1 {
		return nil, fmt.Errorf("N must be >= 1, got %d", n)
	}
	if r < 1 || r > n {
		return nil, fmt.Errorf("R must be between 1 and N (%d), got %d", n, r)
	}
	if w < 1 || w > n {
		return nil, fmt.Errorf("W must be between 1 and N (%d), got %d", n, w)
	}
	return &QuorumConfig{N: n, R: r, W: w}, nil
}

// IsStrong returns true if R + W > N (strong consistency guarantee).
func (q *QuorumConfig) IsStrong() bool {
	return q.R+q.W > q.N
}

// String returns a human-readable description.
func (q *QuorumConfig) String() string {
	consistency := "eventual"
	if q.IsStrong() {
		consistency = "strong"
	}
	return fmt.Sprintf("N=%d R=%d W=%d (%s, R+W=%d)", q.N, q.R, q.W, consistency, q.R+q.W)
}
