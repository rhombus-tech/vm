// core/regional_store.go
package core

import (
    "context"
    "github.com/ava-labs/avalanchego/x/merkledb"
)

// RegionalStore defines interface for region-specific state operations
type RegionalStore interface {
    // Basic state operations
    Get(ctx context.Context, key []byte) ([]byte, error)
    Insert(ctx context.Context, key []byte, value []byte) error
    Delete(ctx context.Context, key []byte) error
    
    // Merkle proof operations
    GetProof(ctx context.Context, key []byte) (*merkledb.Proof, error)
    VerifyProof(ctx context.Context, proof *merkledb.Proof) error
    
    // Additional helper methods
    GetRegionID() string
    GetRoot(ctx context.Context) ([]byte, error)
}