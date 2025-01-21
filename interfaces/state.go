// interfaces/state.go
package interfaces

import (
	"context"

	"github.com/ava-labs/avalanchego/x/merkledb"
)

// RegionalStore interface
type RegionalStore interface {
    // State methods
    GetValue(ctx context.Context, key []byte) ([]byte, error)
    Insert(ctx context.Context, key []byte, value []byte) error
    Remove(ctx context.Context, key []byte) error

    // Regional store methods
    Get(ctx context.Context, key []byte) ([]byte, error)
    Delete(ctx context.Context, key []byte) error  // Make sure this is included
    GetProof(ctx context.Context, key []byte) (*merkledb.Proof, error)
    VerifyProof(ctx context.Context, proof *merkledb.Proof) error
    GetRegionID() string
    GetRoot(ctx context.Context) ([]byte, error)
}