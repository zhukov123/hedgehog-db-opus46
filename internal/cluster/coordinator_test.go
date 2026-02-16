package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hedgehog-db/hedgehog/internal/storage"
)

// mockItemTable is an in-memory table that counts PutItem/GetItem/DeleteItem calls.
type mockItemTable struct {
	data  map[string]map[string]interface{}
	mu    sync.Mutex
	puts  int
	gets  int
	dels  int
}

func newMockItemTable() *mockItemTable {
	return &mockItemTable{data: make(map[string]map[string]interface{})}
}

func (m *mockItemTable) PutItem(key string, doc map[string]interface{}) error {
	m.mu.Lock()
	m.puts++
	m.mu.Unlock()
	m.data[key] = doc
	return nil
}

func (m *mockItemTable) GetItem(key string) (map[string]interface{}, error) {
	m.mu.Lock()
	m.gets++
	m.mu.Unlock()
	if doc, ok := m.data[key]; ok {
		return doc, nil
	}
	return nil, storage.ErrKeyNotFound
}

func (m *mockItemTable) DeleteItem(key string) error {
	m.mu.Lock()
	m.dels++
	m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *mockItemTable) putCount() int  { m.mu.Lock(); defer m.mu.Unlock(); return m.puts }
func (m *mockItemTable) getCount() int  { m.mu.Lock(); defer m.mu.Unlock(); return m.gets }
func (m *mockItemTable) delCount() int  { m.mu.Lock(); defer m.mu.Unlock(); return m.dels }

// mockTableManager implements TableManagerForCoordinator with in-memory tables and call counting.
type mockTableManager struct {
	tables map[string]*mockItemTable
	mu     sync.Mutex
}

func newMockTableManager() *mockTableManager {
	return &mockTableManager{tables: make(map[string]*mockItemTable)}
}

func (m *mockTableManager) GetTable(name string) (ItemTable, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tables[name]; ok {
		return t, nil
	}
	t := newMockItemTable()
	m.tables[name] = t
	return t, nil
}

func (m *mockTableManager) getOrCreate(name string) *mockItemTable {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tables[name]; ok {
		return t
	}
	t := newMockItemTable()
	m.tables[name] = t
	return t
}

func TestCoordinator_RoutePut_Strong_SingleLocalWrite(t *testing.T) {
	// Strong put must use only quorumWrite; when self is the only replica, exactly one local PutItem.
	ring := NewRing(8)
	selfID := "n1"
	mem := NewMembership(selfID, "127.0.0.1:8080", ring, 0, 0)
	ring.AddNode(selfID)

	tm := newMockTableManager()
	_, _ = tm.GetTable("t1") // ensure table exists
	tblMock := tm.getOrCreate("t1")

	coord := NewCoordinator(mem, ring, tm, 1, 1, 1)
	coord.SetReplicator(nil)

	ctx := context.Background()
	err := coord.RoutePut(ctx, "t1", "k1", map[string]interface{}{"x": 2}, "strong")
	if err != nil {
		t.Fatalf("RoutePut strong: %v", err)
	}

	if n := tblMock.putCount(); n != 1 {
		t.Errorf("expected exactly 1 PutItem (strong = quorum only), got %d", n)
	}
}

func TestCoordinator_RouteDelete_Strong_SingleLocalDelete(t *testing.T) {
	ring := NewRing(8)
	selfID := "n1"
	mem := NewMembership(selfID, "127.0.0.1:8080", ring, 0, 0)
	ring.AddNode(selfID)

	tm := newMockTableManager()
	tbl, _ := tm.GetTable("t1")
	_ = tbl.PutItem("k1", map[string]interface{}{"x": 1})
	tblMock := tm.getOrCreate("t1")

	coord := NewCoordinator(mem, ring, tm, 1, 1, 1)
	coord.SetReplicator(nil)

	ctx := context.Background()
	err := coord.RouteDelete(ctx, "t1", "k1", "strong")
	if err != nil {
		t.Fatalf("RouteDelete strong: %v", err)
	}

	if n := tblMock.delCount(); n != 1 {
		t.Errorf("expected exactly 1 DeleteItem (strong = quorum only), got %d", n)
	}
}

func TestCoordinator_RoutePut_Eventual_WhenReplica_OneLocalPut(t *testing.T) {
	ring := NewRing(8)
	selfID := "n1"
	mem := NewMembership(selfID, "127.0.0.1:8080", ring, 0, 0)
	ring.AddNode(selfID)

	tm := newMockTableManager()
	tblMock := tm.getOrCreate("t1")

	coord := NewCoordinator(mem, ring, tm, 1, 1, 1)
	coord.SetReplicator(nil)

	ctx := context.Background()
	err := coord.RoutePut(ctx, "t1", "k1", map[string]interface{}{"a": 1}, "eventual")
	if err != nil {
		t.Fatalf("RoutePut eventual: %v", err)
	}

	if n := tblMock.putCount(); n != 1 {
		t.Errorf("expected 1 local PutItem when replica, got %d", n)
	}
}

func TestCoordinator_RouteDelete_Eventual_WhenReplica_OneLocalDelete(t *testing.T) {
	ring := NewRing(8)
	selfID := "n1"
	mem := NewMembership(selfID, "127.0.0.1:8080", ring, 0, 0)
	ring.AddNode(selfID)

	tm := newMockTableManager()
	tbl, _ := tm.GetTable("t1")
	_ = tbl.PutItem("k1", map[string]interface{}{"x": 1})
	tblMock := tm.getOrCreate("t1")

	coord := NewCoordinator(mem, ring, tm, 1, 1, 1)
	coord.SetReplicator(nil)

	ctx := context.Background()
	err := coord.RouteDelete(ctx, "t1", "k1", "eventual")
	if err != nil {
		t.Fatalf("RouteDelete eventual: %v", err)
	}

	if n := tblMock.delCount(); n != 1 {
		t.Errorf("expected 1 local DeleteItem when replica, got %d", n)
	}
}

func TestCoordinator_RouteGet_Strong_QuorumRead(t *testing.T) {
	ring := NewRing(8)
	selfID := "n1"
	mem := NewMembership(selfID, "127.0.0.1:8080", ring, 0, 0)
	ring.AddNode(selfID)

	tm := newMockTableManager()
	tbl, _ := tm.GetTable("t1")
	_ = tbl.PutItem("k1", map[string]interface{}{"v": 42})

	coord := NewCoordinator(mem, ring, tm, 1, 1, 1)

	ctx := context.Background()
	doc, err := coord.RouteGet(ctx, "t1", "k1", "strong")
	if err != nil {
		t.Fatalf("RouteGet strong: %v", err)
	}
	if doc == nil {
		t.Fatalf("expected doc, got nil")
	}
	if doc["v"] != 42 && doc["v"] != float64(42) {
		t.Errorf("expected doc v=42, got %v", doc)
	}
}

func TestCoordinator_RouteGet_Eventual_LocalRead(t *testing.T) {
	ring := NewRing(8)
	selfID := "n1"
	mem := NewMembership(selfID, "127.0.0.1:8080", ring, 0, 0)
	ring.AddNode(selfID)

	tm := newMockTableManager()
	tbl, _ := tm.GetTable("t1")
	_ = tbl.PutItem("k1", map[string]interface{}{"v": 99})

	coord := NewCoordinator(mem, ring, tm, 1, 1, 1)

	ctx := context.Background()
	doc, err := coord.RouteGet(ctx, "t1", "k1", "eventual")
	if err != nil {
		t.Fatalf("RouteGet eventual: %v", err)
	}
	if doc == nil {
		t.Fatalf("expected doc, got nil")
	}
	if doc["v"] != 99 && doc["v"] != float64(99) {
		t.Errorf("expected doc v=99, got %v", doc)
	}
}

func TestCoordinator_RoutePut_Eventual_WhenNotReplica_ForwardsToPrimary(t *testing.T) {
	// Start a fake replicate endpoint that accepts PUT and returns 200.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Query().Get("op") != "put" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	ring := NewRing(8)
	selfID := "n1"
	otherID := "n2"
	mem := NewMembership(selfID, "127.0.0.1:8080", ring, 0, 0)
	ring.AddNode(selfID)
	mem.AddNode(otherID, addr)
	ring.AddNode(otherID)

	// With replN=1, GetNodes(key, 1) returns exactly one node. Find key where that node is otherID (so we are not replica).
	var key string
	for i := 0; i < 200; i++ {
		key = fmt.Sprintf("key%d", i)
		nodes := ring.GetNodes(key, 1)
		if len(nodes) == 1 && nodes[0] == otherID {
			break
		}
	}
	nodes := ring.GetNodes(key, 1)
	if len(nodes) != 1 || nodes[0] != otherID {
		t.Skip("could not find key that hashes to other node (ring distribution)")
	}

	tm := newMockTableManager()
	tblMock := tm.getOrCreate("t1")

	coord := NewCoordinator(mem, ring, tm, 1, 1, 1)
	coord.SetReplicator(nil)

	ctx := context.Background()
	err := coord.RoutePut(ctx, "t1", key, map[string]interface{}{"x": 1}, "eventual")
	if err != nil {
		t.Fatalf("RoutePut eventual (forward): %v", err)
	}

	// Local table must not have been written (we forwarded).
	if n := tblMock.putCount(); n != 0 {
		t.Errorf("expected 0 local PutItem when not replica (forward), got %d", n)
	}
}

func TestCoordinator_RouteGet_Eventual_WhenNotReplica_ForwardsGet(t *testing.T) {
	// Fake replicate GET endpoint returns a document.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("op") != "get" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"item": map[string]interface{}{"v": 123}})
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	ring := NewRing(8)
	selfID := "n1"
	otherID := "n2"
	mem := NewMembership(selfID, "127.0.0.1:8080", ring, 0, 0)
	ring.AddNode(selfID)
	mem.AddNode(otherID, addr)
	ring.AddNode(otherID)

	var key string
	for i := 0; i < 200; i++ {
		key = fmt.Sprintf("fk%d", i)
		nodes := ring.GetNodes(key, 1)
		if len(nodes) == 1 && nodes[0] == otherID {
			break
		}
	}
	nodes := ring.GetNodes(key, 1)
	if len(nodes) != 1 || nodes[0] != otherID {
		t.Skip("could not find key that hashes to other node (ring distribution)")
	}

	tm := newMockTableManager()
	coord := NewCoordinator(mem, ring, tm, 1, 1, 1)

	ctx := context.Background()
	doc, err := coord.RouteGet(ctx, "t1", key, "eventual")
	if err != nil {
		t.Fatalf("RouteGet eventual (forward): %v", err)
	}
	if doc == nil {
		t.Fatalf("expected doc, got nil")
	}
	if doc["v"] != 123 && doc["v"] != float64(123) {
		t.Errorf("expected forwarded doc v=123, got %v", doc)
	}
}
