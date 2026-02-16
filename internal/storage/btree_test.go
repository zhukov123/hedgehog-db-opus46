package storage

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func setupTestTree(t *testing.T) (*BPlusTree, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hedgehog-test-*")
	if err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, "test.db")
	pager, err := OpenPager(dbPath)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}

	freelist := NewFreeList(pager)
	pool := NewBufferPool(pager, freelist, 256)
	tree, err := OpenBPlusTree(pool, pager, 0)
	if err != nil {
		pager.Close()
		os.RemoveAll(dir)
		t.Fatal(err)
	}

	cleanup := func() {
		tree.Close()
		pager.Close()
		os.RemoveAll(dir)
	}

	return tree, cleanup
}

func TestBPlusTree_InsertAndSearch(t *testing.T) {
	tree, cleanup := setupTestTree(t)
	defer cleanup()

	// Insert a few keys
	keys := []string{"apple", "banana", "cherry", "date", "elderberry"}
	for _, k := range keys {
		if err := tree.Insert([]byte(k), []byte("value-"+k)); err != nil {
			t.Fatalf("Insert %q: %v", k, err)
		}
	}

	// Search for each key
	for _, k := range keys {
		val, err := tree.Search([]byte(k))
		if err != nil {
			t.Fatalf("Search %q: %v", k, err)
		}
		expected := "value-" + k
		if string(val) != expected {
			t.Fatalf("Search %q: got %q, want %q", k, val, expected)
		}
	}

	// Search for non-existent key
	_, err := tree.Search([]byte("fig"))
	if err != ErrKeyNotFound {
		t.Fatalf("Search non-existent: got %v, want ErrKeyNotFound", err)
	}
}

func TestBPlusTree_Update(t *testing.T) {
	tree, cleanup := setupTestTree(t)
	defer cleanup()

	key := []byte("mykey")
	if err := tree.Insert(key, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := tree.Insert(key, []byte("v2")); err != nil {
		t.Fatal(err)
	}

	val, err := tree.Search(key)
	if err != nil {
		t.Fatal(err)
	}
	if string(val) != "v2" {
		t.Fatalf("got %q, want %q", val, "v2")
	}
}

func TestBPlusTree_Delete(t *testing.T) {
	tree, cleanup := setupTestTree(t)
	defer cleanup()

	keys := []string{"a", "b", "c", "d", "e"}
	for _, k := range keys {
		tree.Insert([]byte(k), []byte("val"))
	}

	// Delete 'c'
	if err := tree.Delete([]byte("c")); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify 'c' is gone
	_, err := tree.Search([]byte("c"))
	if err != ErrKeyNotFound {
		t.Fatalf("Search deleted key: got %v, want ErrKeyNotFound", err)
	}

	// Verify others remain
	for _, k := range []string{"a", "b", "d", "e"} {
		if _, err := tree.Search([]byte(k)); err != nil {
			t.Fatalf("Search %q after delete: %v", k, err)
		}
	}

	// Delete non-existent
	err = tree.Delete([]byte("z"))
	if err != ErrKeyNotFound {
		t.Fatalf("Delete non-existent: got %v, want ErrKeyNotFound", err)
	}
}

func TestBPlusTree_ManyKeys(t *testing.T) {
	tree, cleanup := setupTestTree(t)
	defer cleanup()

	n := 1000
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("key-%05d", i)
	}

	// Insert in random order
	perm := rand.Perm(n)
	for _, idx := range perm {
		k := keys[idx]
		if err := tree.Insert([]byte(k), []byte("value-"+k)); err != nil {
			t.Fatalf("Insert %q: %v", k, err)
		}
	}

	// Verify count
	count, err := tree.Count()
	if err != nil {
		t.Fatal(err)
	}
	if count != n {
		t.Fatalf("Count: got %d, want %d", count, n)
	}

	// Verify each key
	for _, k := range keys {
		val, err := tree.Search([]byte(k))
		if err != nil {
			t.Fatalf("Search %q: %v", k, err)
		}
		if string(val) != "value-"+k {
			t.Fatalf("Search %q: got %q", k, val)
		}
	}

	// Verify scan order
	var scanned []string
	tree.Scan(func(key, value []byte) bool {
		scanned = append(scanned, string(key))
		return true
	})

	sortedKeys := make([]string, n)
	copy(sortedKeys, keys)
	sort.Strings(sortedKeys)

	if len(scanned) != n {
		t.Fatalf("Scan count: got %d, want %d", len(scanned), n)
	}
	for i := range scanned {
		if scanned[i] != sortedKeys[i] {
			t.Fatalf("Scan order [%d]: got %q, want %q", i, scanned[i], sortedKeys[i])
		}
	}
}

func TestBPlusTree_DeleteMany(t *testing.T) {
	tree, cleanup := setupTestTree(t)
	defer cleanup()

	n := 500
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("key-%04d", i)
		tree.Insert([]byte(keys[i]), []byte("val"))
	}

	// Delete half
	for i := 0; i < n; i += 2 {
		if err := tree.Delete([]byte(keys[i])); err != nil {
			t.Fatalf("Delete %q: %v", keys[i], err)
		}
	}

	// Verify remaining
	remaining := 0
	for i := 0; i < n; i++ {
		_, err := tree.Search([]byte(keys[i]))
		if i%2 == 0 {
			if err != ErrKeyNotFound {
				t.Fatalf("Deleted key %q still found", keys[i])
			}
		} else {
			if err != nil {
				t.Fatalf("Key %q not found: %v", keys[i], err)
			}
			remaining++
		}
	}
	if remaining != n/2 {
		t.Fatalf("Remaining: %d, want %d", remaining, n/2)
	}
}

func TestBPlusTree_ScanChunked(t *testing.T) {
	tree, cleanup := setupTestTree(t)
	defer cleanup()

	n := 10
	chunkSize := 2
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("key-%03d", i)
		v := fmt.Sprintf("value-%03d", i)
		if err := tree.Insert([]byte(k), []byte(v)); err != nil {
			t.Fatalf("Insert %q: %v", k, err)
		}
	}

	var seen []string
	var lastKey string
	err := tree.ScanChunked(chunkSize, func(key, value []byte) bool {
		sk := string(key)
		seen = append(seen, sk)
		if lastKey != "" && sk <= lastKey {
			t.Errorf("ScanChunked out of order: %q after %q", sk, lastKey)
		}
		lastKey = sk
		return true
	})
	if err != nil {
		t.Fatalf("ScanChunked: %v", err)
	}
	if len(seen) != n {
		t.Fatalf("ScanChunked saw %d items, want %d", len(seen), n)
	}
	for i := 0; i < n; i++ {
		expected := fmt.Sprintf("key-%03d", i)
		if seen[i] != expected {
			t.Fatalf("ScanChunked[%d]: got %q, want %q", i, seen[i], expected)
		}
	}
}

func TestBPlusTree_ScanChunked_ConcurrentSearch(t *testing.T) {
	tree, cleanup := setupTestTree(t)
	defer cleanup()

	for i := 0; i < 20; i++ {
		k := fmt.Sprintf("key-%03d", i)
		tree.Insert([]byte(k), []byte("v"))
	}

	done := make(chan bool)
	go func() {
		_ = tree.ScanChunked(2, func(key, value []byte) bool {
			return true
		})
		done <- true
	}()
	for i := 0; i < 20; i++ {
		k := fmt.Sprintf("key-%03d", i)
		if _, err := tree.Search([]byte(k)); err != nil {
			t.Errorf("Search %q during ScanChunked: %v", k, err)
		}
	}
	<-done
}

func TestBPlusTree_SplitAndMerge(t *testing.T) {
	tree, cleanup := setupTestTree(t)
	defer cleanup()

	// Insert enough keys to cause multiple splits
	n := 200
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("key-%04d", i)
		v := fmt.Sprintf("value-with-some-padding-%04d", i)
		if err := tree.Insert([]byte(k), []byte(v)); err != nil {
			t.Fatalf("Insert %q: %v", k, err)
		}
	}

	// Verify all
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("key-%04d", i)
		val, err := tree.Search([]byte(k))
		if err != nil {
			t.Fatalf("Search %q: %v", k, err)
		}
		expected := fmt.Sprintf("value-with-some-padding-%04d", i)
		if string(val) != expected {
			t.Fatalf("got %q, want %q", val, expected)
		}
	}

	// Delete all
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("key-%04d", i)
		if err := tree.Delete([]byte(k)); err != nil {
			t.Fatalf("Delete %q: %v", k, err)
		}
	}

	count, _ := tree.Count()
	if count != 0 {
		t.Fatalf("Count after delete all: %d", count)
	}
}

func BenchmarkBPlusTree_Insert(b *testing.B) {
	dir, _ := os.MkdirTemp("", "hedgehog-bench-*")
	defer os.RemoveAll(dir)

	dbPath := filepath.Join(dir, "bench.db")
	pager, _ := OpenPager(dbPath)
	freelist := NewFreeList(pager)
	pool := NewBufferPool(pager, freelist, 1024)
	tree, _ := OpenBPlusTree(pool, pager, 0)
	defer func() {
		tree.Close()
		pager.Close()
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%08d", i))
		value := []byte(fmt.Sprintf("value-%08d", i))
		tree.Insert(key, value)
	}
}

func BenchmarkBPlusTree_Search(b *testing.B) {
	dir, _ := os.MkdirTemp("", "hedgehog-bench-*")
	defer os.RemoveAll(dir)

	dbPath := filepath.Join(dir, "bench.db")
	pager, _ := OpenPager(dbPath)
	freelist := NewFreeList(pager)
	pool := NewBufferPool(pager, freelist, 1024)
	tree, _ := OpenBPlusTree(pool, pager, 0)
	defer func() {
		tree.Close()
		pager.Close()
	}()

	n := 10000
	for i := 0; i < n; i++ {
		key := []byte(fmt.Sprintf("key-%08d", i))
		value := []byte(fmt.Sprintf("value-%08d", i))
		tree.Insert(key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%08d", i%n))
		tree.Search(key)
	}
}
