package storage

import (
	"testing"
)

func TestFileHeader_SerializeDeserialize(t *testing.T) {
	h := NewFileHeader()
	h.RootPageID = 42
	h.TotalPages = 100
	h.FreeListHead = 5
	h.WALLSN = 12345

	data := h.Serialize()
	if len(data) != PageSize {
		t.Fatalf("Serialized size: %d, want %d", len(data), PageSize)
	}

	h2, err := DeserializeFileHeader(data)
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}

	if h2.RootPageID != 42 {
		t.Errorf("RootPageID: %d, want 42", h2.RootPageID)
	}
	if h2.TotalPages != 100 {
		t.Errorf("TotalPages: %d, want 100", h2.TotalPages)
	}
	if h2.FreeListHead != 5 {
		t.Errorf("FreeListHead: %d, want 5", h2.FreeListHead)
	}
	if h2.WALLSN != 12345 {
		t.Errorf("WALLSN: %d, want 12345", h2.WALLSN)
	}
	if string(h2.Magic[:]) != MagicString {
		t.Errorf("Magic: %q, want %q", h2.Magic, MagicString)
	}
}

func TestFileHeader_CorruptChecksum(t *testing.T) {
	h := NewFileHeader()
	data := h.Serialize()
	data[33] ^= 0xFF // corrupt checksum
	_, err := DeserializeFileHeader(data)
	if err != ErrCorruptPage {
		t.Fatalf("Expected ErrCorruptPage, got %v", err)
	}
}

func TestPage_LeafCellInsertSearch(t *testing.T) {
	p := NewPage(1, PageTypeLeaf)

	// Insert some keys
	keys := []string{"cherry", "apple", "banana"} // unsorted on purpose
	for _, k := range keys {
		err := p.InsertLeafCell([]byte(k), []byte("val-"+k))
		if err != nil {
			t.Fatalf("InsertLeafCell %q: %v", k, err)
		}
	}

	if p.CellCount() != 3 {
		t.Fatalf("CellCount: %d, want 3", p.CellCount())
	}

	// Search
	for _, k := range keys {
		val, found := p.SearchLeaf([]byte(k))
		if !found {
			t.Fatalf("SearchLeaf %q: not found", k)
		}
		if string(val) != "val-"+k {
			t.Fatalf("SearchLeaf %q: got %q", k, val)
		}
	}

	// Not found
	_, found := p.SearchLeaf([]byte("fig"))
	if found {
		t.Fatal("SearchLeaf 'fig': should not be found")
	}

	// Verify sorted order
	expected := []string{"apple", "banana", "cherry"}
	for i := 0; i < int(p.CellCount()); i++ {
		k := p.GetLeafKeyAt(i)
		if string(k) != expected[i] {
			t.Fatalf("Key order [%d]: got %q, want %q", i, k, expected[i])
		}
	}
}

func TestPage_LeafCellDelete(t *testing.T) {
	p := NewPage(1, PageTypeLeaf)
	p.InsertLeafCell([]byte("a"), []byte("1"))
	p.InsertLeafCell([]byte("b"), []byte("2"))
	p.InsertLeafCell([]byte("c"), []byte("3"))

	if !p.DeleteLeafCell([]byte("b")) {
		t.Fatal("DeleteLeafCell 'b': returned false")
	}

	if p.CellCount() != 2 {
		t.Fatalf("CellCount: %d, want 2", p.CellCount())
	}

	_, found := p.SearchLeaf([]byte("b"))
	if found {
		t.Fatal("'b' should be deleted")
	}

	if _, found := p.SearchLeaf([]byte("a")); !found {
		t.Fatal("'a' should still exist")
	}
	if _, found := p.SearchLeaf([]byte("c")); !found {
		t.Fatal("'c' should still exist")
	}
}

func TestPage_InternalCells(t *testing.T) {
	p := NewPage(1, PageTypeInternal)

	// Insert keys with child pointers
	p.InsertInternalCell(10, []byte("banana"))
	p.InsertInternalCell(20, []byte("apple"))
	p.InsertInternalCell(30, []byte("cherry"))
	p.SetRightPointer(40)

	if p.CellCount() != 3 {
		t.Fatalf("CellCount: %d, want 3", p.CellCount())
	}

	// FindChild tests
	// Key < "apple" -> child 20
	child := p.FindChild([]byte("aaa"))
	if child != 20 {
		t.Fatalf("FindChild 'aaa': got %d, want 20", child)
	}

	// "apple" <= key < "banana" -> child 10
	child = p.FindChild([]byte("avocado"))
	if child != 10 {
		t.Fatalf("FindChild 'avocado': got %d, want 10", child)
	}

	// key >= "cherry" -> right pointer (40)
	child = p.FindChild([]byte("date"))
	if child != 40 {
		t.Fatalf("FindChild 'date': got %d, want 40", child)
	}
}

func TestPage_FreeSpace(t *testing.T) {
	p := NewPage(1, PageTypeLeaf)
	initialFree := p.FreeSpace()

	p.InsertLeafCell([]byte("key"), []byte("value"))
	afterInsert := p.FreeSpace()

	if afterInsert >= initialFree {
		t.Fatalf("Free space should decrease after insert: %d >= %d", afterInsert, initialFree)
	}
}

func TestCompareKeys(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"a", "b", -1},
		{"b", "a", 1},
		{"a", "a", 0},
		{"abc", "abd", -1},
		{"ab", "abc", -1},
		{"abc", "ab", 1},
		{"", "", 0},
		{"", "a", -1},
	}

	for _, tt := range tests {
		result := compareKeys([]byte(tt.a), []byte(tt.b))
		if result != tt.expected {
			t.Errorf("compareKeys(%q, %q) = %d, want %d", tt.a, tt.b, result, tt.expected)
		}
	}
}
