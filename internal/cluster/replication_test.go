package cluster

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestHintedHandoff_AddHint_DrainHints_PendingCount_NodeIDs(t *testing.T) {
	hh := NewHintedHandoff()

	h := hint{TableName: "t1", Key: "k1", Op: "put", Doc: map[string]interface{}{"x": 1}, Timestamp: time.Now()}
	hh.AddHint("n1", h)
	hh.AddHint("n1", hint{TableName: "t1", Key: "k2", Op: "put", Doc: nil, Timestamp: time.Now()})

	if n := hh.PendingCount("n1"); n != 2 {
		t.Errorf("PendingCount(n1) = %d, want 2", n)
	}
	ids := hh.NodeIDs()
	if len(ids) != 1 || ids[0] != "n1" {
		t.Errorf("NodeIDs = %v, want [n1]", ids)
	}

	drained := hh.DrainHints("n1")
	if len(drained) != 2 {
		t.Errorf("DrainHints len = %d, want 2", len(drained))
	}
	if hh.PendingCount("n1") != 0 {
		t.Errorf("after DrainHints PendingCount = %d, want 0", hh.PendingCount("n1"))
	}
}

func TestHintedHandoff_DrainN(t *testing.T) {
	hh := NewHintedHandoff()
	for i := 0; i < 5; i++ {
		hh.AddHint("n1", hint{TableName: "t1", Key: "k", Op: "put", Timestamp: time.Now()})
	}

	batch := hh.DrainN("n1", 2)
	if len(batch) != 2 {
		t.Errorf("DrainN(2) len = %d, want 2", len(batch))
	}
	if hh.PendingCount("n1") != 3 {
		t.Errorf("after DrainN(2) PendingCount = %d, want 3", hh.PendingCount("n1"))
	}

	rest := hh.DrainN("n1", 10)
	if len(rest) != 3 {
		t.Errorf("DrainN(10) len = %d, want 3", len(rest))
	}
	if hh.PendingCount("n1") != 0 {
		t.Errorf("after DrainN(10) PendingCount = %d, want 0", hh.PendingCount("n1"))
	}
}

func TestHintedHandoff_ConcurrentAddAndDrainN(t *testing.T) {
	hh := NewHintedHandoff()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(j int) {
			defer wg.Done()
			hh.AddHint("n1", hint{TableName: "t1", Key: "k", Op: "put", Timestamp: time.Now()})
		}(i)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hh.DrainN("n1", 1)
		}()
	}
	wg.Wait()
	// No panic and final state consistent
	_ = hh.PendingCount("n1")
	_ = hh.NodeIDs()
}

func TestReplicator_ReplicateWrite_OneSuccessOneFail_StoresHint(t *testing.T) {
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okServer.Close()
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	ring := NewRing(8)
	selfID := "self"
	mem := NewMembership(selfID, "127.0.0.1:8080", ring, 0, 0)
	ring.AddNode(selfID)
	mem.AddNode("ok", okServer.Listener.Addr().String())
	mem.AddNode("fail", failServer.Listener.Addr().String())
	ring.AddNode("ok")
	ring.AddNode("fail")

	r := NewReplicator(mem)
	// ReplicateWrite to ok and fail: ok succeeds, fail returns 500 so we store a hint.
	r.ReplicateWrite("t1", "k1", map[string]interface{}{"a": 1}, []string{selfID, "ok", "fail"})

	// Allow async ReplicateWrite to finish
	time.Sleep(100 * time.Millisecond)

	if n := r.Handoff().PendingCount("fail"); n != 1 {
		t.Errorf("expected 1 hint for fail node, got %d", n)
	}
}

func TestReplicator_ReplayHintsBatch_200_RemovesHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ring := NewRing(8)
	selfID := "self"
	mem := NewMembership(selfID, "127.0.0.1:8080", ring, 0, 0)
	ring.AddNode(selfID)
	mem.AddNode("n1", server.Listener.Addr().String())
	ring.AddNode("n1")

	r := NewReplicator(mem)
	r.Handoff().AddHint("n1", hint{
		TableName: "t1", Key: "k1", Op: "put",
		Doc: map[string]interface{}{"x": 1}, Timestamp: time.Now(),
	})

	r.ReplayHintsBatch("n1", 10)

	if n := r.Handoff().PendingCount("n1"); n != 0 {
		t.Errorf("after 200 replay PendingCount = %d, want 0", n)
	}
}

func TestReplicator_ReplayHintsBatch_5xx_ReaddsHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ring := NewRing(8)
	selfID := "self"
	mem := NewMembership(selfID, "127.0.0.1:8080", ring, 0, 0)
	ring.AddNode(selfID)
	mem.AddNode("n1", server.Listener.Addr().String())
	ring.AddNode("n1")

	r := NewReplicator(mem)
	r.Handoff().AddHint("n1", hint{
		TableName: "t1", Key: "k1", Op: "put",
		Doc: map[string]interface{}{"x": 1}, Timestamp: time.Now(),
	})

	r.ReplayHintsBatch("n1", 10)

	// Hint must be re-added on 5xx
	if n := r.Handoff().PendingCount("n1"); n != 1 {
		t.Errorf("after 5xx replay PendingCount = %d, want 1 (hint re-added)", n)
	}
}

func TestReplicator_ReplayHints_5xx_ReaddsHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ring := NewRing(8)
	selfID := "self"
	mem := NewMembership(selfID, "127.0.0.1:8080", ring, 0, 0)
	ring.AddNode(selfID)
	mem.AddNode("n1", server.Listener.Addr().String())
	ring.AddNode("n1")

	r := NewReplicator(mem)
	r.Handoff().AddHint("n1", hint{
		TableName: "t1", Key: "k1", Op: "delete",
		Timestamp: time.Now(),
	})

	r.ReplayHints("n1")

	if n := r.Handoff().PendingCount("n1"); n != 1 {
		t.Errorf("after 5xx ReplayHints PendingCount = %d, want 1 (hint re-added)", n)
	}
}
