// storage/wrapper.go
package storage

import (
    "context"
    "fmt"
    
    "github.com/ava-labs/avalanchego/database"
    "github.com/ava-labs/avalanchego/x/merkledb"
    "github.com/rhombus-tech/vm/coordination"
)

// StorageWrapper implements coordination.BaseStorage interface
type StorageWrapper struct {
    db database.Database
}

// Ensure StorageWrapper implements required interfaces
var _ coordination.BaseStorage = (*StorageWrapper)(nil)
var _ database.Database = (*StorageWrapper)(nil)

func NewStorageWrapper(db database.Database) *StorageWrapper {
    return &StorageWrapper{
        db: db,
    }
}

// Basic storage operations with context
func (sw *StorageWrapper) Put(ctx context.Context, key []byte, value []byte) error {
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        return sw.db.Put(key, value)
    }
}

func (sw *StorageWrapper) Get(ctx context.Context, key []byte) ([]byte, error) {
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    default:
        return sw.db.Get(key)
    }
}

func (sw *StorageWrapper) Delete(ctx context.Context, key []byte) error {
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        return sw.db.Delete(key)
    }
}

// Implement database.Database interface methods
func (sw *StorageWrapper) Close() error {
    return sw.db.Close()
}

func (sw *StorageWrapper) Has(key []byte) (bool, error) {
    return sw.db.Has(key)
}

func (sw *StorageWrapper) NewBatch() database.Batch {
    return sw.db.NewBatch()
}

func (sw *StorageWrapper) NewIterator() database.Iterator {
    return sw.db.NewIterator()
}

func (sw *StorageWrapper) NewIteratorWithPrefix(prefix []byte) database.Iterator {
    return sw.db.NewIteratorWithPrefix(prefix)
}

func (sw *StorageWrapper) NewIteratorWithStartAndPrefix(start, prefix []byte) database.Iterator {
    return sw.db.NewIteratorWithStartAndPrefix(start, prefix)
}

// Additional helper methods for coordination
func (sw *StorageWrapper) GetByPrefix(ctx context.Context, prefix []byte) ([][]byte, error) {
    results := make([][]byte, 0)
    iter := sw.db.NewIteratorWithPrefix(prefix)
    defer iter.Release()

    for iter.Next() {
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        default:
            value := make([]byte, len(iter.Value()))
            copy(value, iter.Value())
            results = append(results, value)
        }
    }
    
    if err := iter.Error(); err != nil {
        return nil, fmt.Errorf("iterator error: %w", err)
    }
    
    return results, nil
}

// Batch operations support
func (sw *StorageWrapper) Batch() *StorageBatch {
    return &StorageBatch{
        batch: sw.db.NewBatch(),
    }
}

// StorageBatch handles batched operations
type StorageBatch struct {
    batch database.Batch
}

func (sb *StorageBatch) Put(key []byte, value []byte) error {
    return sb.batch.Put(key, value)
}

func (sb *StorageBatch) Delete(key []byte) error {
    return sb.batch.Delete(key)
}

func (sb *StorageBatch) Size() int {
    return sb.batch.Size()
}

func (sb *StorageBatch) Write() error {
    return sb.batch.Write()
}

func (sb *StorageBatch) Reset() {
    sb.batch.Reset()
}

func (sb *StorageBatch) Replay(w database.KeyValueWriter) error {
    return sb.batch.Replay(w)
}

// Merkle proof support
func (sw *StorageWrapper) GetProof(ctx context.Context, key []byte) (*merkledb.Proof, error) {
    // This is a placeholder - actual implementation would depend on your merkle tree implementation
    return nil, fmt.Errorf("merkle proofs not implemented")
}

func (sw *StorageWrapper) VerifyProof(ctx context.Context, proof *merkledb.Proof) error {
    // This is a placeholder - actual implementation would depend on your merkle tree implementation
    return fmt.Errorf("merkle proof verification not implemented")
}

// Metrics and monitoring
func (sw *StorageWrapper) Stats() map[string]interface{} {
    return map[string]interface{}{
        "type": "storage_wrapper",
        // Add any relevant stats
    }
}