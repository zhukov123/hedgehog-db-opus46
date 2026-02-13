package table

// EncodeKey encodes a string key into bytes for the B+ tree.
// Keys are stored as raw UTF-8 bytes for lexicographic comparison.
func EncodeKey(key string) []byte {
	return []byte(key)
}

// DecodeKey decodes bytes back to a string key.
func DecodeKey(data []byte) string {
	return string(data)
}
