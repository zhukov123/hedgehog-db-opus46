package table

import (
	"os"
	"testing"
)

func TestCatalog_OpenCreateGetListDrop(t *testing.T) {
	dir, err := os.MkdirTemp("", "hedgehog-catalog-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	catalog, err := OpenCatalog(dir)
	if err != nil {
		t.Fatal(err)
	}

	meta, err := catalog.CreateTable("t1")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "t1" {
		t.Errorf("CreateTable name: got %q, want t1", meta.Name)
	}
	if err := catalog.Save(); err != nil {
		t.Fatal(err)
	}

	got, ok := catalog.GetTable("t1")
	if !ok || got.Name != "t1" {
		t.Errorf("GetTable t1: ok=%v, got %+v", ok, got)
	}
	_, ok = catalog.GetTable("nonexistent")
	if ok {
		t.Error("GetTable nonexistent: expected false")
	}

	list := catalog.ListTables()
	if len(list) != 1 || list[0].Name != "t1" {
		t.Errorf("ListTables: got %v", list)
	}

	if err := catalog.DeleteTable("t1"); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Save(); err != nil {
		t.Fatal(err)
	}
	_, ok = catalog.GetTable("t1")
	if ok {
		t.Error("GetTable t1 after Drop: expected false")
	}
	list = catalog.ListTables()
	if len(list) != 0 {
		t.Errorf("ListTables after drop: got %v", list)
	}
}

func TestCatalog_CreateTable_IdempotentFails(t *testing.T) {
	dir, err := os.MkdirTemp("", "hedgehog-catalog-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	catalog, err := OpenCatalog(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = catalog.CreateTable("t1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalog.CreateTable("t1")
	if err == nil {
		t.Error("second CreateTable(t1): expected error (already exists)")
	}
}

func TestCatalog_Persistence(t *testing.T) {
	dir, err := os.MkdirTemp("", "hedgehog-catalog-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	{
		catalog, err := OpenCatalog(dir)
		if err != nil {
			t.Fatal(err)
		}
		_, err = catalog.CreateTable("persisted")
		if err != nil {
			t.Fatal(err)
		}
		if err := catalog.Save(); err != nil {
			t.Fatal(err)
		}
	}

	catalog, err := OpenCatalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := catalog.GetTable("persisted")
	if !ok {
		t.Fatal("catalog not persisted: table persisted not found")
	}
	if meta.Name != "persisted" {
		t.Errorf("GetTable: got %q", meta.Name)
	}
}
