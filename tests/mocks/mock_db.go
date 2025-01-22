// tests/mocks/mock_db.go
package mocks

import (
    "bytes"
    "context"
    "sort"
    "sync"

    "github.com/ava-labs/avalanchego/database"
)

// Verify interface implementations
var (
    _ database.Database = &MockDB{}
    _ database.Batch = &MockBatch{}
    _ database.Iterator = &MockIterator{}
)

// MockDB implements database.Database
type MockDB struct {
    data  map[string][]byte
    mu    sync.RWMutex
    batch database.Batch
}

type MockBatch struct {
    db      *MockDB
    writes  map[string][]byte
    deletes map[string]struct{}
    size    int
}

func NewMockBatch(db *MockDB) *MockBatch {
    return &MockBatch{
        db:      db,
        writes:  make(map[string][]byte),
        deletes: make(map[string]struct{}),
        size:    0,
    }
}

// NewMockDB creates a new instance of MockDB
func NewMockDB() *MockDB {
    return &MockDB{
        data: make(map[string][]byte),
    }
}

// Required database.Database methods
func (db *MockDB) Get(key []byte) ([]byte, error) {
    db.mu.RLock()
    defer db.mu.RUnlock()
    if value, ok := db.data[string(key)]; ok {
        return value, nil
    }
    return nil, database.ErrNotFound
}

func (db *MockDB) Put(key []byte, value []byte) error {
    db.mu.Lock()
    defer db.mu.Unlock()
    db.data[string(key)] = value
    return nil
}

func (db *MockDB) Delete(key []byte) error {
    db.mu.Lock()
    defer db.mu.Unlock()
    delete(db.data, string(key))
    return nil
}

func (db *MockDB) Has(key []byte) (bool, error) {
    db.mu.RLock()
    defer db.mu.RUnlock()
    _, ok := db.data[string(key)]
    return ok, nil
}

func (db *MockDB) Close() error {
    return nil
}


func (db *MockDB) Compact(start []byte, limit []byte) error {
    return nil // No-op for mock
}


func (db *MockDB) HealthCheck(context.Context) (interface{}, error) {
    return nil, nil
}


// Inner implements the database.Batch interface
func (b *MockBatch) Inner() database.Batch {
    return b
}

func (db *MockDB) NewBatch() database.Batch {
    return &MockBatch{
        db:      db,
        writes:  make(map[string][]byte),
        deletes: make(map[string]struct{}),
        size:    0,
    }
}

func (b *MockBatch) Put(key []byte, value []byte) error {
    b.writes[string(key)] = value
    delete(b.deletes, string(key))
    b.size += len(key) + len(value)
    return nil
}

func (b *MockBatch) Delete(key []byte) error {
    b.deletes[string(key)] = struct{}{}
    delete(b.writes, string(key))
    b.size += len(key)
    return nil
}

func (b *MockBatch) Size() int {
    return b.size
}

func (b *MockBatch) Write() error {
    b.db.mu.Lock()
    defer b.db.mu.Unlock()

    // Apply writes
    for k, v := range b.writes {
        b.db.data[k] = v
    }

    // Apply deletes
    for k := range b.deletes {
        delete(b.db.data, k)
    }

    return nil
}

func (b *MockBatch) Reset() {
    b.writes = make(map[string][]byte)
    b.deletes = make(map[string]struct{})
    b.size = 0
}

func (b *MockBatch) Replay(w database.KeyValueWriterDeleter) error {
    // First process deletes
    for k := range b.deletes {
        if err := w.Delete([]byte(k)); err != nil {
            return err
        }
    }

    // Then process writes
    for k, v := range b.writes {
        if err := w.Put([]byte(k), v); err != nil {
            return err
        }
    }

    return nil
}

// MockIterator implements database.Iterator
type MockIterator struct {
    db       *MockDB
    keys     []string
    current  int
    err      error
    released bool
}

func (db *MockDB) NewIterator() database.Iterator {
    return db.NewIteratorWithStartAndPrefix(nil, nil)
}

func (db *MockDB) NewIteratorWithStart(start []byte) database.Iterator {
    return db.NewIteratorWithStartAndPrefix(start, nil)
}

func (db *MockDB) NewIteratorWithPrefix(prefix []byte) database.Iterator {
    return db.NewIteratorWithStartAndPrefix(nil, prefix)
}

func (db *MockDB) NewIteratorWithStartAndPrefix(start, prefix []byte) database.Iterator {
    db.mu.RLock()
    defer db.mu.RUnlock()

    keys := make([]string, 0, len(db.data))
    for k := range db.data {
        if prefix != nil && !bytes.HasPrefix([]byte(k), prefix) {
            continue
        }
        if start != nil && bytes.Compare([]byte(k), start) < 0 {
            continue
        }
        keys = append(keys, k)
    }
    sort.Strings(keys)

    return &MockIterator{
        db:      db,
        keys:    keys,
        current: -1,
    }
}

func (it *MockIterator) Next() bool {
    if it.released {
        return false
    }
    if it.current >= len(it.keys)-1 {
        return false
    }
    it.current++
    return true
}

func (it *MockIterator) Error() error {
    return it.err
}

func (it *MockIterator) Key() []byte {
    if it.released || it.current < 0 || it.current >= len(it.keys) {
        return nil
    }
    return []byte(it.keys[it.current])
}

func (it *MockIterator) Value() []byte {
    if it.released || it.current < 0 || it.current >= len(it.keys) {
        return nil
    }

    it.db.mu.RLock()
    defer it.db.mu.RUnlock()

    value := it.db.data[it.keys[it.current]]
    result := make([]byte, len(value))
    copy(result, value)
    return result
}

func (it *MockIterator) Release() {
    it.released = true
    it.db = nil
    it.keys = nil
    it.current = -1
}


// Helper method to verify interface implementation
var (
    _ database.Database = &MockDB{}
    _ database.Batch    = &MockBatch{}
    _ database.Iterator = &MockIterator{}
)