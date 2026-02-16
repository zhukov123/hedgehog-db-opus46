package cluster

import (
	"sync"
	"testing"
	"time"
)

func TestMembership_AddNode_RemoveNode_GetNodeAddr_IsAlive(t *testing.T) {
	ring := NewRing(8)
	selfID := "self"
	selfAddr := "127.0.0.1:8080"
	mem := NewMembership(selfID, selfAddr, ring, time.Hour, time.Hour) // long timeouts so no failure detection

	if mem.SelfID() != selfID {
		t.Errorf("SelfID: got %q", mem.SelfID())
	}
	if mem.GetNodeAddr(selfID) != selfAddr {
		t.Errorf("GetNodeAddr(self): got %q", mem.GetNodeAddr(selfID))
	}
	if !mem.IsAlive(selfID) {
		t.Error("IsAlive(self): expected true")
	}

	mem.AddNode("n2", "127.0.0.1:8081")
	if mem.GetNodeAddr("n2") != "127.0.0.1:8081" {
		t.Errorf("GetNodeAddr(n2): got %q", mem.GetNodeAddr("n2"))
	}
	if !mem.IsAlive("n2") {
		t.Error("IsAlive(n2): expected true")
	}

	if mem.GetNodeAddr("nonexistent") != "" {
		t.Error("GetNodeAddr(nonexistent): expected empty")
	}
	if mem.IsAlive("nonexistent") {
		t.Error("IsAlive(nonexistent): expected false")
	}

	if err := mem.RemoveNode("n2"); err != nil {
		t.Fatal(err)
	}
	if mem.GetNodeAddr("n2") != "" {
		t.Errorf("GetNodeAddr(n2) after remove: got %q", mem.GetNodeAddr("n2"))
	}
	if mem.IsAlive("n2") {
		t.Error("IsAlive(n2) after remove: expected false")
	}

	if err := mem.RemoveNode(selfID); err != ErrCannotRemoveSelf {
		t.Errorf("RemoveNode(self): got %v, want ErrCannotRemoveSelf", err)
	}
}

func TestMembership_HandleHeartbeat_UpdatesState(t *testing.T) {
	ring := NewRing(8)
	mem := NewMembership("self", "127.0.0.1:8080", ring, time.Hour, time.Hour)
	mem.AddNode("n2", "127.0.0.1:8081")

	// Simulate heartbeat from n2 with new address
	mem.HandleHeartbeat("n2", "127.0.0.1:9090")
	if addr := mem.GetNodeAddr("n2"); addr != "127.0.0.1:9090" {
		t.Errorf("HandleHeartbeat: GetNodeAddr(n2)=%q, want 127.0.0.1:9090", addr)
	}
	if !mem.IsAlive("n2") {
		t.Error("HandleHeartbeat: n2 should be alive")
	}
}

func TestMembership_SetRecoveryCallback_CalledWhenNodeRecovers(t *testing.T) {
	ring := NewRing(8)
	// Short timeouts so we can drive node to Dead then recover
	heartbeatInterval := 5 * time.Millisecond
	failureTimeout := 30 * time.Millisecond
	mem := NewMembership("self", "127.0.0.1:8080", ring, heartbeatInterval, failureTimeout)
	mem.AddNode("n2", "127.0.0.1:8081")

	var recovered string
	var wg sync.WaitGroup
	wg.Add(1)
	mem.SetRecoveryCallback(func(nodeID string) {
		recovered = nodeID
		wg.Done()
	})

	mem.Start()
	defer mem.Stop()

	// Wait for n2 to be marked Suspect then Dead (no heartbeats from n2 to self)
	time.Sleep(failureTimeout*2 + 20*time.Millisecond)

	// Simulate n2 recovering: send heartbeat
	mem.HandleHeartbeat("n2", "127.0.0.1:8081")

	// Callback should fire (node was Dead/Suspect, now Alive)
	wg.Wait()
	if recovered != "n2" {
		t.Errorf("RecoveryCallback: got nodeID %q, want n2", recovered)
	}
}
