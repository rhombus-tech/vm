// storage/wrapper.go
package storage

import (
    "context"
    
    "github.com/ava-labs/avalanchego/database"
    "github.com/rhombus-tech/vm/coordination"
)

// StorageWrapper implements both database.Database and coordination.BaseStorage
type StorageWrapper struct {
    db database.Database
}

// Ensure StorageWrapper implements required interfaces
var _ database.Database = (*StorageWrapper)(nil)

func NewStorageWrapper(db database.Database) *StorageWrapper {
    return &StorageWrapper{
        db: db,
    }
}

// database.Database interface methods
func (sw *StorageWrapper) Put(key []byte, value []byte) error {
    return sw.db.Put(key, value)
}

func (sw *StorageWrapper) Get(key []byte) ([]byte, error) {
    return sw.db.Get(key)
}

func (sw *StorageWrapper) Delete(key []byte) error {
    return sw.db.Delete(key)
}

func (sw *StorageWrapper) Has(key []byte) (bool, error) {
    return sw.db.Has(key)
}

func (sw *StorageWrapper) Close() error {
    return sw.db.Close()
}

func (sw *StorageWrapper) NewBatch() database.Batch {
    return sw.db.NewBatch()
}

func (sw *StorageWrapper) NewIterator() database.Iterator {
    return sw.db.NewIterator()
}

func (sw *StorageWrapper) NewIteratorWithStart(start []byte) database.Iterator {
    return sw.db.NewIteratorWithStart(start)
}

func (sw *StorageWrapper) NewIteratorWithPrefix(prefix []byte) database.Iterator {
    return sw.db.NewIteratorWithPrefix(prefix)
}

func (sw *StorageWrapper) NewIteratorWithStartAndPrefix(start, prefix []byte) database.Iterator {
    return sw.db.NewIteratorWithStartAndPrefix(start, prefix)
}

// coordination.BaseStorage interface methods
func (sw *StorageWrapper) PutWithContext(ctx context.Context, key []byte, value []byte) error {
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        return sw.db.Put(key, value)
    }
}

func (sw *StorageWrapper) GetWithContext(ctx context.Context, key []byte) ([]byte, error) {
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    default:
        return sw.db.Get(key)
    }
}

func (sw *StorageWrapper) DeleteWithContext(ctx context.Context, key []byte) error {
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        return sw.db.Delete(key)
    }
}

func (sw *StorageWrapper) GetByPrefix(ctx context.Context, prefix []byte) ([][]byte, error) {
    iter := sw.db.NewIteratorWithPrefix(prefix)
    defer iter.Release()

    var results [][]byte
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
    return results, iter.Error()
}

// Additional helper methods
func (sw *StorageWrapper) Compact(start []byte, limit []byte) error {
    if compacter, ok := sw.db.(interface{ Compact([]byte, []byte) error }); ok {
        return compacter.Compact(start, limit)
    }
    return nil
}

func (sw *StorageWrapper) HealthCheck(ctx context.Context) (interface{}, error) {
    if healthChecker, ok := sw.db.(interface{ HealthCheck(context.Context) (interface{}, error) }); ok {
        return healthChecker.HealthCheck(ctx)
    }
    return nil, nil
}

// StorageBatch implementation
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

func (sb *StorageBatch) Replay(w database.KeyValueWriterDeleter) error {
    return sb.batch.Replay(w)
}

// Create a CoordinationStorageWrapper for use with coordination package
type CoordinationStorageWrapper struct {
    db database.Database
}

func NewCoordinationStorageWrapper(db database.Database) *CoordinationStorageWrapper {
    return &CoordinationStorageWrapper{
        db: db,
    }
}

// Implement coordination.BaseStorage interface
func (csw *CoordinationStorageWrapper) Put(ctx context.Context, key []byte, value []byte) error {
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        return csw.db.Put(key, value)
    }
}

func (csw *CoordinationStorageWrapper) Get(ctx context.Context, key []byte) ([]byte, error) {
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    default:
        return csw.db.Get(key)
    }
}

func (csw *CoordinationStorageWrapper) Delete(ctx context.Context, key []byte) error {
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        return csw.db.Delete(key)
    }
}

func (csw *CoordinationStorageWrapper) GetByPrefix(ctx context.Context, prefix []byte) ([][]byte, error) {
    iter := csw.db.NewIteratorWithPrefix(prefix)
    defer iter.Release()

    var results [][]byte
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
    return results, iter.Error()
}

// Verify interface implementation
var _ coordination.BaseStorage = (*CoordinationStorageWrapper)(nil)