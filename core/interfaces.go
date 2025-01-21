// core/interfaces.go
package core

import (
	"context"

	"github.com/ava-labs/avalanchego/x/merkledb"
	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/state"
)

// StateManager extends chain.StateManager
type StateManager interface {
    chain.StateManager
    
    // Object operations
    GetObject(ctx context.Context, mu state.Mutable, id string) (*ObjectState, error)
    SetObject(ctx context.Context, mu state.Mutable, id string, obj *ObjectState) error
    ObjectExists(ctx context.Context, mu state.Mutable, id string) (bool, error)
    
    // Event operations
    SetEvent(ctx context.Context, mu state.Mutable, id string, event *Event) error
    
    // Region operations
    GetRegion(ctx context.Context, mu state.Mutable, id string) (map[string]interface{}, error)
    SetRegion(ctx context.Context, mu state.Mutable, id string, region map[string]interface{}) error
    RegionExists(ctx context.Context, mu state.Mutable, id string) (bool, error)
    
    // Input object operations
    SetInputObject(ctx context.Context, mu state.Mutable, id string) error
    
    // Coordination
    GetCoordinator() Coordinator

    GetKeysByPrefix(ctx context.Context, prefix []byte) ([][]byte, error)

    GetRegionalStore(regionID string) (RegionalStore, error)
    IsRegionalKey(key []byte) (bool, string)
     
}

// Coordinator interface
type Coordinator interface {
    SubmitTask(ctx context.Context, task *Task) error
    SendMessage(msg *Message) error
    GetWorkerIDs() []WorkerID
}

type StorageView interface {
    GetValue(ctx context.Context, key []byte) ([]byte, error)
    Insert(ctx context.Context, key []byte, value []byte) error
    Delete(ctx context.Context, key []byte) error
    Commit() error
}

type CoordinationStorage interface {
    NewView(ctx context.Context) (StorageView, error)
    GetValue(ctx context.Context, key []byte) ([]byte, error)
    Insert(ctx context.Context, key []byte, value []byte) error
    Delete(ctx context.Context, key []byte) error
    GetProof(ctx context.Context, key []byte) (*merkledb.Proof, error)
}