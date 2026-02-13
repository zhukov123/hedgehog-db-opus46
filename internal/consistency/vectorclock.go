package consistency

import (
	"encoding/json"
	"sync"
	"time"
)

// VectorClock tracks causal ordering across nodes.
type VectorClock struct {
	Clocks    map[string]uint64 `json:"clocks"`
	Timestamp time.Time         `json:"timestamp"` // wall clock for LWW fallback
	mu        sync.RWMutex
}

// NewVectorClock creates a new vector clock.
func NewVectorClock() *VectorClock {
	return &VectorClock{
		Clocks:    make(map[string]uint64),
		Timestamp: time.Now(),
	}
}

// Increment increments the clock for a node.
func (vc *VectorClock) Increment(nodeID string) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.Clocks[nodeID]++
	vc.Timestamp = time.Now()
}

// Merge merges another vector clock into this one (take max of each component).
func (vc *VectorClock) Merge(other *VectorClock) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	other.mu.RLock()
	defer other.mu.RUnlock()

	for nodeID, otherClock := range other.Clocks {
		if otherClock > vc.Clocks[nodeID] {
			vc.Clocks[nodeID] = otherClock
		}
	}
}

// Compare returns the causal relationship between two vector clocks.
// Returns:
//
//	-1 if vc < other (vc happened before other)
//	 0 if vc || other (concurrent, neither dominates)
//	 1 if vc > other (vc happened after other)
func (vc *VectorClock) Compare(other *VectorClock) int {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	other.mu.RLock()
	defer other.mu.RUnlock()

	vcLess := false
	otherLess := false

	// Check all keys in vc
	for nodeID, clock := range vc.Clocks {
		otherClock := other.Clocks[nodeID]
		if clock < otherClock {
			vcLess = true
		}
		if clock > otherClock {
			otherLess = true
		}
	}

	// Check keys only in other
	for nodeID, otherClock := range other.Clocks {
		if _, ok := vc.Clocks[nodeID]; !ok {
			if otherClock > 0 {
				vcLess = true
			}
		}
	}

	if vcLess && !otherLess {
		return -1
	}
	if otherLess && !vcLess {
		return 1
	}
	return 0 // concurrent
}

// Clone creates a deep copy.
func (vc *VectorClock) Clone() *VectorClock {
	vc.mu.RLock()
	defer vc.mu.RUnlock()

	clone := &VectorClock{
		Clocks:    make(map[string]uint64, len(vc.Clocks)),
		Timestamp: vc.Timestamp,
	}
	for k, v := range vc.Clocks {
		clone.Clocks[k] = v
	}
	return clone
}

// Serialize serializes the vector clock to JSON bytes.
func (vc *VectorClock) Serialize() ([]byte, error) {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	return json.Marshal(vc)
}

// DeserializeVectorClock deserializes a vector clock from JSON bytes.
func DeserializeVectorClock(data []byte) (*VectorClock, error) {
	vc := &VectorClock{
		Clocks: make(map[string]uint64),
	}
	if err := json.Unmarshal(data, vc); err != nil {
		return nil, err
	}
	return vc, nil
}

// ResolveConcurrent resolves concurrent writes using last-writer-wins.
func ResolveConcurrent(a, b *VectorClock) *VectorClock {
	cmp := a.Compare(b)
	switch cmp {
	case 1:
		return a
	case -1:
		return b
	default:
		// Concurrent: use wall clock (LWW)
		if a.Timestamp.After(b.Timestamp) {
			return a
		}
		return b
	}
}
