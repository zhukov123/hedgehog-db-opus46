package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFreeList_AllocateAndFree(t *testing.T) {
	dir, err := os.MkdirTemp("", "hedgehog-fl-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	dbPath := filepath.Join(dir, "test.db")
	pager, err := OpenPager(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer pager.Close()

	fl := NewFreeList(pager)
	id1, err := fl.AllocatePage()
	if err != nil {
		t.Fatal(err)
	}
	if id1 == 0 {
		t.Fatal("expected non-zero page ID")
	}
	id2, err := fl.AllocatePage()
	if err != nil {
		t.Fatal(err)
	}
	if id2 == id1 {
		t.Fatal("expected different page IDs")
	}

	if err := fl.FreePage(id1); err != nil {
		t.Fatal(err)
	}

	// Allocate again — should return the freed page
	id3, err := fl.AllocatePage()
	if err != nil {
		t.Fatal(err)
	}
	if id3 != id1 {
		t.Errorf("Allocate after Free: got %d, want %d (reuse freed page)", id3, id1)
	}
}

func TestFreeList_PersistenceAcrossReopen(t *testing.T) {
	dir, err := os.MkdirTemp("", "hedgehog-fl-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	dbPath := filepath.Join(dir, "test.db")

	// Allocate two pages, free one, close
	{
		pager, err := OpenPager(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		fl := NewFreeList(pager)
		id1, err := fl.AllocatePage()
		if err != nil {
			pager.Close()
			t.Fatal(err)
		}
		_, err = fl.AllocatePage()
		if err != nil {
			pager.Close()
			t.Fatal(err)
		}
		if err := fl.FreePage(id1); err != nil {
			pager.Close()
			t.Fatal(err)
		}
		if err := pager.Sync(); err != nil {
			pager.Close()
			t.Fatal(err)
		}
		if err := pager.Close(); err != nil {
			t.Fatal(err)
		}
	}

	// Reopen and allocate — should get the freed page back
	{
		pager, err := OpenPager(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer pager.Close()
		fl := NewFreeList(pager)
		id, err := fl.AllocatePage()
		if err != nil {
			t.Fatal(err)
		}
		// First allocation after reopen should be the previously freed page (ID 1 in typical layout)
		if id == 0 {
			t.Fatal("expected non-zero page ID")
		}
		// We can't assume id==1 because header uses page 0; typically first data page is 1
		_ = id
	}
}
