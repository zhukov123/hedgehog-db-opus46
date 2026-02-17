package table

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TableMeta holds metadata about a table.
type TableMeta struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	ItemCount int64     `json:"item_count"`
}

// Catalog manages table metadata. Stored as a JSON file for simplicity,
// with a B+ tree used for the actual data.
type Catalog struct {
	dataDir string
	tables  map[string]*TableMeta
	mu      sync.RWMutex
}

// OpenCatalog loads or creates the table catalog.
func OpenCatalog(dataDir string) (*Catalog, error) {
	c := &Catalog{
		dataDir: dataDir,
		tables:  make(map[string]*TableMeta),
	}

	catalogPath := filepath.Join(dataDir, "catalog.json")
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("read catalog: %w", err)
	}

	if len(data) > 0 {
		if err := json.Unmarshal(data, &c.tables); err != nil {
			return nil, fmt.Errorf("parse catalog: %w", err)
		}
	}

	return c, nil
}

// Save persists the catalog to disk.
func (c *Catalog) Save() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := json.MarshalIndent(c.tables, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal catalog: %w", err)
	}

	catalogPath := filepath.Join(c.dataDir, "catalog.json")
	return os.WriteFile(catalogPath, data, 0644)
}

// CreateTable registers a new table.
func (c *Catalog) CreateTable(name string) (*TableMeta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.tables[name]; exists {
		return nil, fmt.Errorf("table %q already exists", name)
	}

	meta := &TableMeta{
		Name:      name,
		CreatedAt: time.Now(),
	}
	c.tables[name] = meta
	return meta, nil
}

// GetTable returns metadata for a table.
func (c *Catalog) GetTable(name string) (*TableMeta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	meta, ok := c.tables[name]
	return meta, ok
}

// ListTables returns all table metadata.
func (c *Catalog) ListTables() []*TableMeta {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*TableMeta, 0, len(c.tables))
	for _, meta := range c.tables {
		result = append(result, meta)
	}
	return result
}

// DeleteTable removes a table from the catalog.
func (c *Catalog) DeleteTable(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.tables[name]; !exists {
		return fmt.Errorf("table %q not found", name)
	}

	delete(c.tables, name)
	return nil
}

// UpdateItemCount updates the item count for a table.
func (c *Catalog) UpdateItemCount(name string, count int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if meta, ok := c.tables[name]; ok {
		meta.ItemCount = count
	}
}

// TableManager manages open tables with lazy loading and caching.
type TableManager struct {
	catalog *Catalog
	dataDir string
	tables  map[string]*Table
	mu      sync.RWMutex
	opts    TableOptions
}

// NewTableManager creates a new table manager.
func NewTableManager(catalog *Catalog, opts TableOptions) *TableManager {
	return &TableManager{
		catalog: catalog,
		dataDir: opts.DataDir,
		tables:  make(map[string]*Table),
		opts:    opts,
	}
}

// GetTable returns an open table, opening it if necessary.
func (tm *TableManager) GetTable(name string) (*Table, error) {
	tm.mu.RLock()
	if t, ok := tm.tables[name]; ok {
		tm.mu.RUnlock()
		return t, nil
	}
	tm.mu.RUnlock()

	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Double-check after acquiring write lock
	if t, ok := tm.tables[name]; ok {
		return t, nil
	}

	// Verify table exists in catalog
	if _, ok := tm.catalog.GetTable(name); !ok {
		return nil, fmt.Errorf("table %q not found", name)
	}

	t, err := OpenTable(name, tm.opts)
	if err != nil {
		return nil, err
	}

	tm.tables[name] = t
	return t, nil
}

// CreateTable creates a new table and opens it.
func (tm *TableManager) CreateTable(name string) (*Table, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if _, err := tm.catalog.CreateTable(name); err != nil {
		return nil, err
	}

	if err := tm.catalog.Save(); err != nil {
		return nil, err
	}

	t, err := OpenTable(name, tm.opts)
	if err != nil {
		return nil, err
	}

	tm.tables[name] = t
	return t, nil
}

// DeleteTable closes and deletes a table.
func (tm *TableManager) DeleteTable(name string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if t, ok := tm.tables[name]; ok {
		t.Close()
		delete(tm.tables, name)
	}

	if err := tm.catalog.DeleteTable(name); err != nil {
		return err
	}

	if err := tm.catalog.Save(); err != nil {
		return err
	}

	// Remove data files (legacy or partitioned)
	dbPath := filepath.Join(tm.dataDir, name+".db")
	walPath := filepath.Join(tm.dataDir, name+".wal")
	os.Remove(dbPath)
	os.Remove(walPath)
	for i := 0; i < 32; i++ {
		pDb := filepath.Join(tm.dataDir, fmt.Sprintf("%s_p%d.db", name, i))
		pWal := filepath.Join(tm.dataDir, fmt.Sprintf("%s_p%d.wal", name, i))
		if _, err := os.Stat(pDb); os.IsNotExist(err) {
			break
		}
		os.Remove(pDb)
		os.Remove(pWal)
	}

	return nil
}

// Close closes all open tables.
func (tm *TableManager) Close() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	var lastErr error
	for name, t := range tm.tables {
		if err := t.Close(); err != nil {
			lastErr = err
		}
		delete(tm.tables, name)
	}
	return lastErr
}

// Catalog returns the catalog.
func (tm *TableManager) Catalog() *Catalog {
	return tm.catalog
}
