package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/hedgehog-db/hedgehog/internal/storage"
	"github.com/hedgehog-db/hedgehog/internal/table"
)

// Handlers holds the API handler dependencies.
type Handlers struct {
	tableManager *table.TableManager
}

// NewHandlers creates API handlers.
func NewHandlers(tm *table.TableManager) *Handlers {
	return &Handlers{tableManager: tm}
}

// --- Table Operations ---

// CreateTable handles POST /api/v1/tables
func (h *Handlers) CreateTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "table name is required")
		return
	}

	_, err := h.tableManager.CreateTable(req.Name)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"name":    req.Name,
		"message": "table created",
	})
}

// ListTables handles GET /api/v1/tables
func (h *Handlers) ListTables(w http.ResponseWriter, r *http.Request) {
	tables := h.tableManager.Catalog().ListTables()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tables": tables,
	})
}

// DeleteTable handles DELETE /api/v1/tables/{name}
func (h *Handlers) DeleteTable(w http.ResponseWriter, r *http.Request) {
	name := Param(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "table name is required")
		return
	}

	if err := h.tableManager.DeleteTable(name); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": fmt.Sprintf("table %q deleted", name),
	})
}

// --- Item Operations ---

// GetItem handles GET /api/v1/tables/{name}/items/{key}
func (h *Handlers) GetItem(w http.ResponseWriter, r *http.Request) {
	tableName := Param(r, "name")
	key := Param(r, "key")

	t, err := h.tableManager.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	doc, err := t.GetItem(key)
	if err != nil {
		if err == storage.ErrKeyNotFound {
			writeError(w, http.StatusNotFound, fmt.Sprintf("item %q not found", key))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"key":  key,
		"item": doc,
	})
}

// PutItem handles PUT /api/v1/tables/{name}/items/{key}
func (h *Handlers) PutItem(w http.ResponseWriter, r *http.Request) {
	tableName := Param(r, "name")
	key := Param(r, "key")

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	var doc map[string]interface{}
	if err := json.Unmarshal(body, &doc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON document")
		return
	}

	t, err := h.tableManager.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if err := t.PutItem(key, doc); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"key":     key,
		"message": "item saved",
	})
}

// DeleteItem handles DELETE /api/v1/tables/{name}/items/{key}
func (h *Handlers) DeleteItem(w http.ResponseWriter, r *http.Request) {
	tableName := Param(r, "name")
	key := Param(r, "key")

	t, err := h.tableManager.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if err := t.DeleteItem(key); err != nil {
		if err == storage.ErrKeyNotFound {
			writeError(w, http.StatusNotFound, fmt.Sprintf("item %q not found", key))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": fmt.Sprintf("item %q deleted", key),
	})
}

// TableCount handles GET /api/v1/tables/{name}/count
func (h *Handlers) TableCount(w http.ResponseWriter, r *http.Request) {
	tableName := Param(r, "name")

	t, err := h.tableManager.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	n, err := t.Count()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"count": n})
}

// ScanItems handles GET /api/v1/tables/{name}/items
func (h *Handlers) ScanItems(w http.ResponseWriter, r *http.Request) {
	tableName := Param(r, "name")

	t, err := h.tableManager.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	items := make([]map[string]interface{}, 0)
	err = t.Scan(func(key string, doc map[string]interface{}) bool {
		items = append(items, map[string]interface{}{
			"key":  key,
			"item": doc,
		})
		return true
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items": items,
		"count": len(items),
	})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{
		"error": message,
	})
}
