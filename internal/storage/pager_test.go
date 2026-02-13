package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPager_CreateAndReopen(t *testing.T) {
	dir, err := os.MkdirTemp("", "hedgehog-pager-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	dbPath := filepath.Join(dir, "test.db")

	// Create new database
	pager, err := OpenPager(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	if pager.Header().TotalPages != 1 {
		t.Fatalf("TotalPages: %d, want 1", pager.Header().TotalPages)
	}

	// Allocate a page
	pageID, err := pager.AllocatePage()
	if err != nil {
		t.Fatal(err)
	}
	if pageID != 1 {
		t.Fatalf("pageID: %d, want 1", pageID)
	}

	// Write data to the page
	page := NewPage(pageID, PageTypeLeaf)
	page.InsertLeafCell([]byte("hello"), []byte("world"))
	if err := pager.WritePage(page); err != nil {
		t.Fatal(err)
	}

	pager.Close()

	// Reopen
	pager2, err := OpenPager(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer pager2.Close()

	if pager2.Header().TotalPages != 2 {
		t.Fatalf("TotalPages after reopen: %d, want 2", pager2.Header().TotalPages)
	}

	// Read the page back
	page2, err := pager2.ReadPage(1)
	if err != nil {
		t.Fatal(err)
	}

	val, found := page2.SearchLeaf([]byte("hello"))
	if !found {
		t.Fatal("Key 'hello' not found after reopen")
	}
	if string(val) != "world" {
		t.Fatalf("got %q, want %q", val, "world")
	}
}

func TestPager_AllocateMultiple(t *testing.T) {
	dir, _ := os.MkdirTemp("", "hedgehog-pager-test-*")
	defer os.RemoveAll(dir)

	pager, _ := OpenPager(filepath.Join(dir, "test.db"))
	defer pager.Close()

	for i := uint32(1); i <= 10; i++ {
		pageID, err := pager.AllocatePage()
		if err != nil {
			t.Fatal(err)
		}
		if pageID != i {
			t.Fatalf("pageID: %d, want %d", pageID, i)
		}
	}

	if pager.TotalPages() != 11 {
		t.Fatalf("TotalPages: %d, want 11", pager.TotalPages())
	}
}
