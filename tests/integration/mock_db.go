// tests/integration/mock_db.go
package integration

import (
    "bytes"
    "context"
    "sort"
    "sync"

    "github.com/ava-labs/avalanchego/database"
)

// MockDB implements database.Database
type MockDB struct {
    data map[string][]byte
    mu   sync.RWMutex
    batch database.Batch
}

func NewMockDB() *MockDB {
    db := &MockDB{
         make(map[string][]byte),
    }
    db.batch = NewMockBatch(db)
    return db
}

// Required database.Database methods
func (db *MockDB) Get(key []byte) ([]byte, error) {
    db.mu.RLock()
    defer db.mu.RUnlock()
    
    if value, ok := db.data[string(key)]; ok {
        result := make([]byte, len(value))
        copy(result, value)
        return result, nil
    }
    return nil, database.ErrNotFound
}

func (db *MockDB) Put(key []byte, value []byte) error {
    db.mu.Lock()
    defer db.mu.Unlock()
    
    valueCopy := make([]byte, len(value))
    copy(valueCopy, value)
    db.data[string(key)] = valueCopy
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

func (db *MockDB) NewBatch() database.Batch {
    return NewMockBatch(db)
}

func (db *MockDB) NewIterator() database.Iterator {
    return NewMockIterator(db, nil)
}

func (db *MockDB) NewIteratorWithStart(start []byte) database.Iterator {
    return NewMockIterator(db, start)
}

func (db *MockDB) NewIteratorWithPrefix(prefix []byte) database.Iterator {
    return NewMockIterator(db, prefix)
}

func (db *MockDB) Compact(start []byte, limit []byte) error {
    return nil // No-op for mock
}

func (db *MockDB) NewIteratorWithStartAndPrefix(start, prefix []byte) database.Iterator {
    return NewMockIterator(db, prefix)
}

func (db *MockDB) HealthCheck(context.Context) (interface{}, error) {
    return nil, nil
}

// MockBatch implementation
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
    }
}

func (b *MockBatch) Put(key []byte, value []byte) error {
    b.writes[string(key)] = value
    b.size += len(key) + len(value)
    return nil
}

func (b *MockBatch) Delete(key []byte) error {
    b.deletes[string(key)] = struct{}{}
    b.size += len(key)
    return nil
}

func (b *MockBatch) Size() int {
    return b.size
}

func (b *MockBatch) Write() error {
    b.db.mu.Lock()
    defer b.db.mu.Unlock()
    
    for k, v := range b.writes {
        b.db.data[k] = v
    }
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
    for k, v := range b.writes {
        if err := w.Put([]byte(k), v); err != nil {
            return err
        }
    }
    for k := range b.deletes {
        if err := w.Delete([]byte(k)); err != nil {
            return err
        }
    }
    return nil
}

func (b *MockBatch) Inner() database.Batch {
    return b
}

// MockIterator implementation
type MockIterator struct {
    db       *MockDB
    keys     []string
    values   [][]byte
    current  int
    prefix   []byte
    released bool
    err      error
}

func NewMockIterator(db *MockDB, prefix []byte) *MockIterator {
    db.mu.RLock()
    defer db.mu.RUnlock()

    iter := &MockIterator{
        db:      db,
        current: -1,
        prefix:  prefix,
    }

    for k, v := range db.data {
        if prefix == nil || bytes.HasPrefix([]byte(k), prefix) {
            iter.keys = append(iter.keys, k)
            iter.values = append(iter.values, v)
        }
    }

    sort.Strings(iter.keys)
    return iter
}

func (it *MockIterator) Next() bool {
    if it.released {
        return false
    }
    it.current++
    return it.current < len(it.keys)
}

func (it *MockIterator) Error() error {
    return it.err
}

func (it *MockIterator) Key() []byte {
    if it.current >= 0 && it.current < len(it.keys) {
        return []byte(it.keys[it.current])
    }
    return nil
}

func (it *MockIterator) Value() []byte {
    if it.current >= 0 && it.current < len(it.values) {
        return it.values[it.current]
    }
    return nil
}

func (it *MockIterator) Release() {
    it.released = true
}