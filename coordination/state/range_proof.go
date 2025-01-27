package state

import (
	"bytes"

	"github.com/ava-labs/avalanchego/x/merkledb"
)

// RangeProof represents a proof of a range of key-value pairs in the merkle tree
type RangeProof struct {
	StartKey []byte                `json:"start_key"`
	EndKey   []byte                `json:"end_key"`
	Entries  map[string][]byte     `json:"entries"`
	Proof    *merkledb.RangeProof  `json:"proof"`
}

// IsInRange checks if a key is within the specified range
func IsInRange(key, start, end []byte) bool {
	return bytes.Compare(key, start) >= 0 && (end == nil || bytes.Compare(key, end) <= 0)
}
