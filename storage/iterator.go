// storage/iterator.go
package storage

import (
	"bytes"
	"context"

	"github.com/ava-labs/avalanchego/database"
	"github.com/rhombus-tech/vm/interfaces"
)

type RegionIterator struct {
    ctx     context.Context
    iter    database.Iterator
    prefix  []byte
    err     error
    current struct {
        key   []byte
        value []byte
    }
}

var _ interfaces.Iterator = &RegionIterator{}

func NewRegionIterator(ctx context.Context, db database.Database, prefix []byte) *RegionIterator {
    return &RegionIterator{
        ctx:    ctx,
        iter:   db.NewIteratorWithPrefix(prefix),
        prefix: prefix,
    }
}



// Implement interfaces.Iterator methods
func (i *RegionIterator) Next() bool {
    if i.err != nil {
        return false
    }

    select {
    case <-i.ctx.Done():
        i.err = i.ctx.Err()
        return false
    default:
    }

    if !i.iter.Next() {
        return false
    }

    key := i.iter.Key()
    if !bytes.HasPrefix(key, i.prefix) {
        return false
    }

    i.current.key = make([]byte, len(key))
    copy(i.current.key, key)

    value := i.iter.Value()
    i.current.value = make([]byte, len(value))
    copy(i.current.value, value)

    return true
}

func (i *RegionIterator) Key() []byte {
    if i.current.key == nil {
        return nil
    }
    result := make([]byte, len(i.current.key))
    copy(result, i.current.key)
    return result
}

func (i *RegionIterator) Value() []byte {
    if i.current.value == nil {
        return nil
    }
    result := make([]byte, len(i.current.value))
    copy(result, i.current.value)
    return result
}

func (i *RegionIterator) Error() error {
    if i.err != nil {
        return i.err
    }
    return i.iter.Error()
}

func (i *RegionIterator) Close() {
    i.iter.Release()
}
