// interfaces/state.go
package interfaces

import (
	"context"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/x/merkledb"
)

// RegionalStore interface
type RegionalStore interface {
    Get(ctx context.Context, key []byte) ([]byte, error)
    Put(ctx context.Context, key []byte, value []byte) error
    Delete(ctx context.Context, key []byte) error
    GetProof(ctx context.Context, key []byte) (*merkledb.Proof, error)
    GetRoot(ctx context.Context) (ids.ID, error)
}