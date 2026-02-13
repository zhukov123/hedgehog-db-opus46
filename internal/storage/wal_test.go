package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWAL_WriteAndRecover(t *testing.T) {
	dir, err := os.MkdirTemp("", "hedgehog-wal-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	walPath := filepath.Join(dir, "test.wal")

	// Write some records
	wal, err := OpenWAL(walPath)
	if err != nil {
		t.Fatal(err)
	}

	txID := wal.BeginTx()
	pageData := make([]byte, PageSize)
	copy(pageData, []byte("test page data"))

	_, err = wal.LogPageWrite(txID, 1, pageData)
	if err != nil {
		t.Fatal(err)
	}

	_, err = wal.LogCommit(txID)
	if err != nil {
		t.Fatal(err)
	}

	// Uncommitted transaction
	txID2 := wal.BeginTx()
	wal.LogPageWrite(txID2, 2, pageData)
	// No commit for txID2

	wal.Close()

	// Recover
	wal2, err := OpenWAL(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer wal2.Close()

	recovered, err := wal2.Recover()
	if err != nil {
		t.Fatal(err)
	}

	// Only committed txn should be recovered
	if _, ok := recovered[1]; !ok {
		t.Fatal("Page 1 should be in recovered data")
	}
	if _, ok := recovered[2]; ok {
		t.Fatal("Page 2 should NOT be in recovered data (uncommitted)")
	}
}

func TestWAL_Checkpoint(t *testing.T) {
	dir, err := os.MkdirTemp("", "hedgehog-wal-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	walPath := filepath.Join(dir, "test.wal")
	wal, err := OpenWAL(walPath)
	if err != nil {
		t.Fatal(err)
	}

	txID := wal.BeginTx()
	wal.LogPageWrite(txID, 1, make([]byte, 100))
	wal.LogCommit(txID)
	wal.LogCheckpoint()

	// New transaction after checkpoint
	txID2 := wal.BeginTx()
	wal.LogPageWrite(txID2, 3, make([]byte, 100))
	wal.LogCommit(txID2)

	wal.Close()

	// Recover should only see post-checkpoint data
	wal2, err := OpenWAL(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer wal2.Close()

	recovered, err := wal2.Recover()
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := recovered[1]; ok {
		t.Fatal("Page 1 should NOT be recovered (before checkpoint)")
	}
	if _, ok := recovered[3]; !ok {
		t.Fatal("Page 3 should be recovered (after checkpoint)")
	}
}

func TestWALRecord_SerializeDeserialize(t *testing.T) {
	record := &WALRecord{
		LSN:    42,
		TxID:   7,
		Type:   WALRecordPageWrite,
		PageID: 13,
		Data:   []byte("hello world"),
	}

	data := record.Serialize()

	// Verify CRC was set
	if record.CRC == 0 {
		t.Fatal("CRC should be non-zero")
	}

	// Read back using a bytes reader
	r := &bytesReader{data: data, pos: 0}
	decoded, err := DeserializeWALRecord(r)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.LSN != 42 || decoded.TxID != 7 || decoded.Type != WALRecordPageWrite ||
		decoded.PageID != 13 || string(decoded.Data) != "hello world" {
		t.Fatalf("Decoded record mismatch: %+v", decoded)
	}
}

type bytesReader struct {
	data []byte
	pos  int
}

func (r *bytesReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, os.ErrClosed
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
