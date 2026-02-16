package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/hedgehog-db/hedgehog/internal/table"
)

func setupHandlersTest(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hedgehog-api-*")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := table.OpenCatalog(dir)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	opts := table.TableOptions{DataDir: dir, BufferPoolSize: 256}
	tm := table.NewTableManager(catalog, opts)
	server := NewServer(":0", tm, nil)
	ts := httptest.NewServer(server.Router())
	cleanup := func() {
		ts.Close()
		tm.Close()
		os.RemoveAll(dir)
	}
	return ts, cleanup
}

func TestHandlers_GetItemPutItem_NilCoordinator(t *testing.T) {
	ts, cleanup := setupHandlersTest(t)
	defer cleanup()

	// Create table
	resp, _ := http.Post(ts.URL+"/api/v1/tables", "application/json", bytes.NewReader([]byte(`{"name":"t1"}`)))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create table: %d", resp.StatusCode)
	}

	// Put item
	body := []byte(`{"x":1}`)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tables/t1/items/k1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put item: %d", resp.StatusCode)
	}

	// Get item
	resp, _ = http.Get(ts.URL + "/api/v1/tables/t1/items/k1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get item: %d", resp.StatusCode)
	}
	var out struct {
		Key  string                 `json:"key"`
		Item map[string]interface{} `json:"item"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out.Key != "k1" || out.Item["x"] != float64(1) {
		t.Errorf("get item: %+v", out)
	}
}

func TestHandlers_GetItem_ConsistencyParam(t *testing.T) {
	ts, cleanup := setupHandlersTest(t)
	defer cleanup()
	resp, _ := http.Post(ts.URL+"/api/v1/tables", "application/json", bytes.NewReader([]byte(`{"name":"t1"}`)))
	resp.Body.Close()
	http.DefaultClient.Do(mustPut(ts.URL+"/api/v1/tables/t1/items/k1", `{"a":1}`))

	// GET with ?consistency=strong (handlers pass through; nil coordinator so same behavior)
	resp, _ = http.Get(ts.URL + "/api/v1/tables/t1/items/k1?consistency=strong")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get with consistency=strong: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHandlers_PutItem_ConsistencyParam(t *testing.T) {
	ts, cleanup := setupHandlersTest(t)
	defer cleanup()
	resp, _ := http.Post(ts.URL+"/api/v1/tables", "application/json", bytes.NewReader([]byte(`{"name":"t1"}`)))
	resp.Body.Close()

	req := mustPut(ts.URL+"/api/v1/tables/t1/items/k1?consistency=eventual", `{"b":2}`)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put with consistency=eventual: %d", resp.StatusCode)
	}
}

func TestHandlers_GetItem_404_MissingTable(t *testing.T) {
	ts, cleanup := setupHandlersTest(t)
	defer cleanup()
	resp, _ := http.Get(ts.URL + "/api/v1/tables/nosuch/items/k1")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("get missing table: %d, want 404", resp.StatusCode)
	}
}

func TestHandlers_GetItem_404_MissingKey(t *testing.T) {
	ts, cleanup := setupHandlersTest(t)
	defer cleanup()
	resp, _ := http.Post(ts.URL+"/api/v1/tables", "application/json", bytes.NewReader([]byte(`{"name":"t1"}`)))
	resp.Body.Close()
	resp, _ = http.Get(ts.URL + "/api/v1/tables/t1/items/nonexistent")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("get missing key: %d, want 404", resp.StatusCode)
	}
}

func TestHandlers_PutItem_InvalidJSON(t *testing.T) {
	ts, cleanup := setupHandlersTest(t)
	defer cleanup()
	resp, _ := http.Post(ts.URL+"/api/v1/tables", "application/json", bytes.NewReader([]byte(`{"name":"t1"}`)))
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tables/t1/items/k1", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("put invalid JSON: %d, want 400", resp.StatusCode)
	}
}

func TestHandlers_DeleteItem_NilCoordinator(t *testing.T) {
	ts, cleanup := setupHandlersTest(t)
	defer cleanup()
	resp, _ := http.Post(ts.URL+"/api/v1/tables", "application/json", bytes.NewReader([]byte(`{"name":"t1"}`)))
	resp.Body.Close()
	http.DefaultClient.Do(mustPut(ts.URL+"/api/v1/tables/t1/items/k1", `{"x":1}`))

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/tables/t1/items/k1", nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete item: %d", resp.StatusCode)
	}
	resp, _ = http.Get(ts.URL + "/api/v1/tables/t1/items/k1")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("get after delete: %d, want 404", resp.StatusCode)
	}
}

func mustPut(url, body string) *http.Request {
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	return req
}
