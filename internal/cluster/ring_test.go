package cluster

import (
	"fmt"
	"testing"
)

func TestRing_AddAndGetNode(t *testing.T) {
	ring := NewRing(128)

	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")

	if ring.Size() != 3 {
		t.Fatalf("Size: %d, want 3", ring.Size())
	}

	// Every key should map to some node
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key-%d", i)
		node := ring.GetNode(key)
		if node == "" {
			t.Fatalf("GetNode(%q) returned empty", key)
		}
	}
}

func TestRing_GetNodes(t *testing.T) {
	ring := NewRing(128)

	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")

	nodes := ring.GetNodes("test-key", 3)
	if len(nodes) != 3 {
		t.Fatalf("GetNodes returned %d nodes, want 3", len(nodes))
	}

	// All should be distinct
	seen := make(map[string]bool)
	for _, n := range nodes {
		if seen[n] {
			t.Fatalf("Duplicate node: %s", n)
		}
		seen[n] = true
	}
}

func TestRing_GetNodes_LessThanN(t *testing.T) {
	ring := NewRing(128)
	ring.AddNode("node-1")
	ring.AddNode("node-2")

	// Request 3 but only 2 exist
	nodes := ring.GetNodes("key", 3)
	if len(nodes) != 2 {
		t.Fatalf("GetNodes: got %d, want 2", len(nodes))
	}
}

func TestRing_RemoveNode(t *testing.T) {
	ring := NewRing(128)
	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")

	ring.RemoveNode("node-2")

	if ring.Size() != 2 {
		t.Fatalf("Size after remove: %d, want 2", ring.Size())
	}

	// No key should map to node-2
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key-%d", i)
		node := ring.GetNode(key)
		if node == "node-2" {
			t.Fatalf("Key %q still maps to removed node-2", key)
		}
	}
}

func TestRing_Distribution(t *testing.T) {
	ring := NewRing(128)
	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")

	counts := make(map[string]int)
	total := 10000
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("user-%d", i)
		node := ring.GetNode(key)
		counts[node]++
	}

	// Each node should get roughly 1/3 of keys (within 20% tolerance)
	expected := total / 3
	tolerance := expected / 5
	for node, count := range counts {
		if count < expected-tolerance || count > expected+tolerance {
			t.Logf("Node %s: %d keys (expected ~%d, tolerance %d)", node, count, expected, tolerance)
		}
	}
}

func TestRing_Consistency(t *testing.T) {
	ring := NewRing(128)
	ring.AddNode("node-1")
	ring.AddNode("node-2")

	// Record mappings
	mappings := make(map[string]string)
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key-%d", i)
		mappings[key] = ring.GetNode(key)
	}

	// Add a node
	ring.AddNode("node-3")

	// Most keys should still map to the same node
	changed := 0
	for key, oldNode := range mappings {
		newNode := ring.GetNode(key)
		if newNode != oldNode {
			changed++
		}
	}

	// With consistent hashing, roughly 1/3 of keys should move
	maxExpectedChanges := 50
	if changed > maxExpectedChanges {
		t.Logf("Warning: %d/100 keys changed when adding a node (expected ~33)", changed)
	}
}
