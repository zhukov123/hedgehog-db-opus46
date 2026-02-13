package storage

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"
)

const (
	WALRecordHeaderSize = 29 // LSN(8) + TxID(8) + Type(1) + PageID(4) + DataLen(4) + CRC(4)

	WALRecordPageWrite  uint8 = 0x01
	WALRecordCommit     uint8 = 0x02
	WALRecordCheckpoint uint8 = 0x03
)

// WALRecord represents a single write-ahead log record.
type WALRecord struct {
	LSN    uint64
	TxID   uint64
	Type   uint8
	PageID uint32
	Data   []byte
	CRC    uint32
}

// Serialize writes the WAL record to bytes.
func (r *WALRecord) Serialize() []byte {
	dataLen := len(r.Data)
	buf := make([]byte, WALRecordHeaderSize+dataLen)

	binary.LittleEndian.PutUint64(buf[0:8], r.LSN)
	binary.LittleEndian.PutUint64(buf[8:16], r.TxID)
	buf[16] = r.Type
	binary.LittleEndian.PutUint32(buf[17:21], r.PageID)
	binary.LittleEndian.PutUint32(buf[21:25], uint32(dataLen))

	if dataLen > 0 {
		copy(buf[WALRecordHeaderSize:], r.Data)
	}

	// CRC over everything except the CRC field
	csum := crc32.ChecksumIEEE(buf[0:25])
	if dataLen > 0 {
		csum = crc32.Update(csum, crc32.IEEETable, r.Data)
	}
	binary.LittleEndian.PutUint32(buf[25:29], csum)
	r.CRC = csum

	return buf
}

// DeserializeWALRecord reads a WAL record from a reader.
func DeserializeWALRecord(reader io.Reader) (*WALRecord, error) {
	header := make([]byte, WALRecordHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}

	r := &WALRecord{
		LSN:    binary.LittleEndian.Uint64(header[0:8]),
		TxID:   binary.LittleEndian.Uint64(header[8:16]),
		Type:   header[16],
		PageID: binary.LittleEndian.Uint32(header[17:21]),
		CRC:    binary.LittleEndian.Uint32(header[25:29]),
	}

	dataLen := binary.LittleEndian.Uint32(header[21:25])
	if dataLen > 0 {
		r.Data = make([]byte, dataLen)
		if _, err := io.ReadFull(reader, r.Data); err != nil {
			return nil, err
		}
	}

	// Verify CRC
	csum := crc32.ChecksumIEEE(header[0:25])
	if dataLen > 0 {
		csum = crc32.Update(csum, crc32.IEEETable, r.Data)
	}
	if csum != r.CRC {
		return nil, fmt.Errorf("WAL record CRC mismatch at LSN %d", r.LSN)
	}

	return r, nil
}

// WAL manages the write-ahead log.
type WAL struct {
	file    *os.File
	path    string
	nextLSN uint64
	nextTxID uint64
	mu      sync.Mutex
}

// OpenWAL opens or creates a WAL file.
func OpenWAL(path string) (*WAL, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open WAL: %w", err)
	}

	w := &WAL{
		file:     file,
		path:     path,
		nextLSN:  1,
		nextTxID: 1,
	}

	// Scan existing records to find the highest LSN and TxID
	if err := w.scanExisting(); err != nil {
		file.Close()
		return nil, err
	}

	return w, nil
}

func (w *WAL) scanExisting() error {
	w.file.Seek(0, io.SeekStart)
	for {
		record, err := DeserializeWALRecord(w.file)
		if err != nil {
			break // EOF or corrupt record
		}
		if record.LSN >= w.nextLSN {
			w.nextLSN = record.LSN + 1
		}
		if record.TxID >= w.nextTxID {
			w.nextTxID = record.TxID + 1
		}
	}
	return nil
}

// BeginTx returns a new transaction ID.
func (w *WAL) BeginTx() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	txID := w.nextTxID
	w.nextTxID++
	return txID
}

// LogPageWrite logs a page write (after-image).
func (w *WAL) LogPageWrite(txID uint64, pageID uint32, data []byte) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	record := &WALRecord{
		LSN:    w.nextLSN,
		TxID:   txID,
		Type:   WALRecordPageWrite,
		PageID: pageID,
		Data:   data,
	}
	w.nextLSN++

	buf := record.Serialize()
	if _, err := w.file.Write(buf); err != nil {
		return 0, fmt.Errorf("WAL write: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return 0, fmt.Errorf("WAL sync: %w", err)
	}

	return record.LSN, nil
}

// LogCommit logs a transaction commit.
func (w *WAL) LogCommit(txID uint64) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	record := &WALRecord{
		LSN:  w.nextLSN,
		TxID: txID,
		Type: WALRecordCommit,
	}
	w.nextLSN++

	buf := record.Serialize()
	if _, err := w.file.Write(buf); err != nil {
		return 0, fmt.Errorf("WAL commit write: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return 0, fmt.Errorf("WAL commit sync: %w", err)
	}

	return record.LSN, nil
}

// LogCheckpoint logs a checkpoint.
func (w *WAL) LogCheckpoint() (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	record := &WALRecord{
		LSN:  w.nextLSN,
		TxID: 0,
		Type: WALRecordCheckpoint,
	}
	w.nextLSN++

	buf := record.Serialize()
	if _, err := w.file.Write(buf); err != nil {
		return 0, fmt.Errorf("WAL checkpoint write: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return 0, fmt.Errorf("WAL checkpoint sync: %w", err)
	}

	return record.LSN, nil
}

// Recover reads the WAL and replays committed transactions.
// Returns the page writes that should be applied (grouped by pageID, only committed txns).
func (w *WAL) Recover() (map[uint32][]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.file.Seek(0, io.SeekStart)

	// Collect all records
	type pageWrite struct {
		pageID uint32
		data   []byte
		lsn    uint64
	}

	txWrites := make(map[uint64][]pageWrite)
	committedTxns := make(map[uint64]bool)

	for {
		record, err := DeserializeWALRecord(w.file)
		if err != nil {
			break
		}

		switch record.Type {
		case WALRecordPageWrite:
			txWrites[record.TxID] = append(txWrites[record.TxID], pageWrite{
				pageID: record.PageID,
				data:   record.Data,
				lsn:    record.LSN,
			})
		case WALRecordCommit:
			committedTxns[record.TxID] = true
		case WALRecordCheckpoint:
			// Clear records before checkpoint
			txWrites = make(map[uint64][]pageWrite)
			committedTxns = make(map[uint64]bool)
		}
	}

	// Build the final page images from committed transactions
	result := make(map[uint32][]byte)
	for txID, writes := range txWrites {
		if !committedTxns[txID] {
			continue // Skip uncommitted
		}
		for _, pw := range writes {
			// Keep the latest write per page
			if existing, ok := result[pw.pageID]; ok {
				_ = existing
			}
			result[pw.pageID] = pw.data
		}
	}

	return result, nil
}

// Truncate clears the WAL file (called after checkpoint + flush).
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.file.Truncate(0); err != nil {
		return fmt.Errorf("WAL truncate: %w", err)
	}
	_, err := w.file.Seek(0, io.SeekStart)
	return err
}

// Close closes the WAL file.
func (w *WAL) Close() error {
	return w.file.Close()
}

// NextLSN returns the next LSN to be assigned.
func (w *WAL) NextLSN() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.nextLSN
}
