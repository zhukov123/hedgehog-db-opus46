package consistency

import (
	"testing"
)

func TestVectorClock_Increment(t *testing.T) {
	vc := NewVectorClock()
	vc.Increment("node-1")
	vc.Increment("node-1")
	vc.Increment("node-2")

	if vc.Clocks["node-1"] != 2 {
		t.Fatalf("node-1: %d, want 2", vc.Clocks["node-1"])
	}
	if vc.Clocks["node-2"] != 1 {
		t.Fatalf("node-2: %d, want 1", vc.Clocks["node-2"])
	}
}

func TestVectorClock_Compare(t *testing.T) {
	// a < b (a happened before b)
	a := NewVectorClock()
	a.Clocks = map[string]uint64{"n1": 1, "n2": 2}

	b := NewVectorClock()
	b.Clocks = map[string]uint64{"n1": 2, "n2": 3}

	if a.Compare(b) != -1 {
		t.Fatalf("a < b: got %d, want -1", a.Compare(b))
	}
	if b.Compare(a) != 1 {
		t.Fatalf("b > a: got %d, want 1", b.Compare(a))
	}

	// Concurrent
	c := NewVectorClock()
	c.Clocks = map[string]uint64{"n1": 3, "n2": 1}

	d := NewVectorClock()
	d.Clocks = map[string]uint64{"n1": 1, "n2": 3}

	if c.Compare(d) != 0 {
		t.Fatalf("c || d: got %d, want 0", c.Compare(d))
	}

	// Equal
	e := NewVectorClock()
	e.Clocks = map[string]uint64{"n1": 5}
	f := NewVectorClock()
	f.Clocks = map[string]uint64{"n1": 5}

	if e.Compare(f) != 0 {
		t.Fatalf("e == f: got %d, want 0", e.Compare(f))
	}
}

func TestVectorClock_Merge(t *testing.T) {
	a := NewVectorClock()
	a.Clocks = map[string]uint64{"n1": 3, "n2": 1}

	b := NewVectorClock()
	b.Clocks = map[string]uint64{"n1": 1, "n2": 5, "n3": 2}

	a.Merge(b)

	expected := map[string]uint64{"n1": 3, "n2": 5, "n3": 2}
	for k, v := range expected {
		if a.Clocks[k] != v {
			t.Fatalf("Merge %s: got %d, want %d", k, a.Clocks[k], v)
		}
	}
}

func TestVectorClock_Clone(t *testing.T) {
	a := NewVectorClock()
	a.Clocks = map[string]uint64{"n1": 5, "n2": 3}

	b := a.Clone()
	b.Increment("n1")

	if a.Clocks["n1"] != 5 {
		t.Fatal("Clone should be independent")
	}
	if b.Clocks["n1"] != 6 {
		t.Fatal("Clone increment failed")
	}
}

func TestVectorClock_SerializeDeserialize(t *testing.T) {
	vc := NewVectorClock()
	vc.Clocks = map[string]uint64{"n1": 10, "n2": 20}

	data, err := vc.Serialize()
	if err != nil {
		t.Fatal(err)
	}

	vc2, err := DeserializeVectorClock(data)
	if err != nil {
		t.Fatal(err)
	}

	if vc2.Clocks["n1"] != 10 || vc2.Clocks["n2"] != 20 {
		t.Fatalf("Deserialized: %v", vc2.Clocks)
	}
}

func TestQuorumConfig(t *testing.T) {
	q, err := NewQuorumConfig(3, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !q.IsStrong() {
		t.Fatal("N=3 R=2 W=2 should be strong consistency")
	}

	q2, err := NewQuorumConfig(3, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if q2.IsStrong() {
		t.Fatal("N=3 R=1 W=1 should be eventual consistency")
	}

	_, err = NewQuorumConfig(3, 0, 2)
	if err == nil {
		t.Fatal("R=0 should be invalid")
	}
}
