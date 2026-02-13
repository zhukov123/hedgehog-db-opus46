package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/hedgehog-db/hedgehog/internal/api"
	"github.com/hedgehog-db/hedgehog/internal/table"
)

func setupTestServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "hedgehog-integration-*")
	if err != nil {
		t.Fatal(err)
	}

	catalog, err := table.OpenCatalog(dir)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}

	opts := table.TableOptions{
		DataDir:        dir,
		BufferPoolSize: 256,
	}
	tm := table.NewTableManager(catalog, opts)

	server := api.NewServer(":0", tm)
	ts := httptest.NewServer(server.Router())

	cleanup := func() {
		ts.Close()
		tm.Close()
		os.RemoveAll(dir)
	}

	return ts, cleanup
}

func TestAPI_TableCRUD(t *testing.T) {
	ts, cleanup := setupTestServer(t)
	defer cleanup()

	// Create table
	resp := doPost(t, ts, "/api/v1/tables", map[string]string{"name": "users"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Create table: status %d", resp.StatusCode)
	}

	// List tables
	resp = doGet(t, ts, "/api/v1/tables")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("List tables: status %d", resp.StatusCode)
	}

	var listResult struct {
		Tables []struct {
			Name string `json:"name"`
		} `json:"tables"`
	}
	json.NewDecoder(resp.Body).Decode(&listResult)
	resp.Body.Close()

	if len(listResult.Tables) != 1 || listResult.Tables[0].Name != "users" {
		t.Fatalf("List tables: %+v", listResult)
	}

	// Create duplicate should fail
	resp = doPost(t, ts, "/api/v1/tables", map[string]string{"name": "users"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("Duplicate table: status %d, want 409", resp.StatusCode)
	}
	resp.Body.Close()

	// Delete table
	resp = doDelete(t, ts, "/api/v1/tables/users")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Delete table: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify deleted
	resp = doGet(t, ts, "/api/v1/tables")
	json.NewDecoder(resp.Body).Decode(&listResult)
	resp.Body.Close()
	if len(listResult.Tables) != 0 {
		t.Fatalf("Tables should be empty after delete")
	}
}

func TestAPI_ItemCRUD(t *testing.T) {
	ts, cleanup := setupTestServer(t)
	defer cleanup()

	// Create table
	resp := doPost(t, ts, "/api/v1/tables", map[string]string{"name": "products"})
	resp.Body.Close()

	// Put item
	item := map[string]interface{}{
		"name":  "Widget",
		"price": 9.99,
	}
	resp = doPut(t, ts, "/api/v1/tables/products/items/widget-1", item)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Put item: status %d, body: %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// Get item
	resp = doGet(t, ts, "/api/v1/tables/products/items/widget-1")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Get item: status %d, body: %s", resp.StatusCode, body)
	}

	var getResult struct {
		Key  string                 `json:"key"`
		Item map[string]interface{} `json:"item"`
	}
	json.NewDecoder(resp.Body).Decode(&getResult)
	resp.Body.Close()

	if getResult.Key != "widget-1" {
		t.Fatalf("Get key: %q", getResult.Key)
	}
	if getResult.Item["name"] != "Widget" {
		t.Fatalf("Get item name: %v", getResult.Item["name"])
	}

	// Update item
	item["price"] = 14.99
	resp = doPut(t, ts, "/api/v1/tables/products/items/widget-1", item)
	resp.Body.Close()

	// Verify update
	resp = doGet(t, ts, "/api/v1/tables/products/items/widget-1")
	json.NewDecoder(resp.Body).Decode(&getResult)
	resp.Body.Close()
	if getResult.Item["price"] != 14.99 {
		t.Fatalf("Updated price: %v", getResult.Item["price"])
	}

	// Delete item
	resp = doDelete(t, ts, "/api/v1/tables/products/items/widget-1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Delete item: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify deleted
	resp = doGet(t, ts, "/api/v1/tables/products/items/widget-1")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Get deleted item: status %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_ScanItems(t *testing.T) {
	ts, cleanup := setupTestServer(t)
	defer cleanup()

	doPost(t, ts, "/api/v1/tables", map[string]string{"name": "items"}).Body.Close()

	// Insert multiple items
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("item-%02d", i)
		doc := map[string]interface{}{
			"index": i,
			"label": fmt.Sprintf("Item %d", i),
		}
		doPut(t, ts, "/api/v1/tables/items/items/"+key, doc).Body.Close()
	}

	// Scan
	resp := doGet(t, ts, "/api/v1/tables/items/items")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Scan: status %d", resp.StatusCode)
	}

	var scanResult struct {
		Items []map[string]interface{} `json:"items"`
		Count int                      `json:"count"`
	}
	json.NewDecoder(resp.Body).Decode(&scanResult)
	resp.Body.Close()

	if scanResult.Count != 10 {
		t.Fatalf("Scan count: %d, want 10", scanResult.Count)
	}
}

func TestAPI_NonExistentTable(t *testing.T) {
	ts, cleanup := setupTestServer(t)
	defer cleanup()

	resp := doGet(t, ts, "/api/v1/tables/nonexistent/items/key1")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Get from non-existent table: status %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_Health(t *testing.T) {
	ts, cleanup := setupTestServer(t)
	defer cleanup()

	resp := doGet(t, ts, "/api/v1/health")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Health: status %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// HTTP helpers

func doGet(t *testing.T, ts *httptest.Server, path string) *http.Response {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func doPost(t *testing.T, ts *httptest.Server, path string, body interface{}) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func doPut(t *testing.T, ts *httptest.Server, path string, body interface{}) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	return resp
}

func doDelete(t *testing.T, ts *httptest.Server, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	return resp
}
