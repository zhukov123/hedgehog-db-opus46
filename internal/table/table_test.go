package table

import (
	"os"
	"testing"

	"github.com/hedgehog-db/hedgehog/internal/storage"
)

func setupTable(t *testing.T, name string) (*Table, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hedgehog-table-*")
	if err != nil {
		t.Fatal(err)
	}
	opts := TableOptions{DataDir: dir, BufferPoolSize: 128}
	tbl, err := OpenTable(name, opts)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	cleanup := func() {
		tbl.Close()
		os.RemoveAll(dir)
	}
	return tbl, cleanup
}

func TestTable_PutItemGetItem(t *testing.T) {
	tbl, cleanup := setupTable(t, "test")
	defer cleanup()

	doc := map[string]interface{}{"name": "alice", "count": 42}
	if err := tbl.PutItem("user1", doc); err != nil {
		t.Fatal(err)
	}
	got, err := tbl.GetItem("user1")
	if err != nil {
		t.Fatal(err)
	}
	if got["name"] != "alice" {
		t.Errorf("GetItem name: got %v", got["name"])
	}
	if got["count"] != float64(42) && got["count"] != 42 {
		t.Errorf("GetItem count: got %v", got["count"])
	}
}

func TestTable_GetItem_NotFound(t *testing.T) {
	tbl, cleanup := setupTable(t, "test")
	defer cleanup()

	_, err := tbl.GetItem("nonexistent")
	if err != storage.ErrKeyNotFound {
		t.Errorf("GetItem nonexistent: got %v, want ErrKeyNotFound", err)
	}
}

func TestTable_DeleteItem(t *testing.T) {
	tbl, cleanup := setupTable(t, "test")
	defer cleanup()

	tbl.PutItem("k1", map[string]interface{}{"x": 1})
	if err := tbl.DeleteItem("k1"); err != nil {
		t.Fatal(err)
	}
	_, err := tbl.GetItem("k1")
	if err != storage.ErrKeyNotFound {
		t.Errorf("GetItem after delete: got %v, want ErrKeyNotFound", err)
	}
}

func TestTable_Scan(t *testing.T) {
	tbl, cleanup := setupTable(t, "test")
	defer cleanup()

	tbl.PutItem("a", map[string]interface{}{"n": 1})
	tbl.PutItem("b", map[string]interface{}{"n": 2})
	tbl.PutItem("c", map[string]interface{}{"n": 3})

	var keys []string
	err := tbl.Scan(func(key string, doc map[string]interface{}) bool {
		keys = append(keys, key)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 {
		t.Fatalf("Scan: got %d keys, want 3", len(keys))
	}
	if keys[0] != "a" || keys[1] != "b" || keys[2] != "c" {
		t.Errorf("Scan order: got %v", keys)
	}
}

func TestTable_ScanChunked(t *testing.T) {
	tbl, cleanup := setupTable(t, "test")
	defer cleanup()

	for i := 0; i < 10; i++ {
		k := string(rune('a' + i))
		tbl.PutItem(k, map[string]interface{}{"i": i})
	}

	var keys []string
	err := tbl.ScanChunked(3, func(key string, doc map[string]interface{}) bool {
		keys = append(keys, key)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 10 {
		t.Fatalf("ScanChunked: got %d keys, want 10", len(keys))
	}
	for i := 0; i < 10; i++ {
		expected := string(rune('a' + i))
		if keys[i] != expected {
			t.Errorf("ScanChunked[%d]: got %q, want %q", i, keys[i], expected)
		}
	}
}

func TestTable_JSONRoundTrip(t *testing.T) {
	tbl, cleanup := setupTable(t, "test")
	defer cleanup()

	doc := map[string]interface{}{
		"string": "hello",
		"number": 42,
		"float":  3.14,
		"bool":   true,
		"nested": map[string]interface{}{"a": 1},
		"array":  []interface{}{1, 2, 3},
	}
	if err := tbl.PutItem("k", doc); err != nil {
		t.Fatal(err)
	}
	got, err := tbl.GetItem("k")
	if err != nil {
		t.Fatal(err)
	}
	if got["string"] != "hello" || got["number"] != float64(42) && got["number"] != 42 {
		t.Errorf("round-trip: got %v", got)
	}
	if g, _ := got["nested"].(map[string]interface{}); g == nil || g["a"] != float64(1) && g["a"] != 1 {
		t.Errorf("nested: got %v", got["nested"])
	}
}
