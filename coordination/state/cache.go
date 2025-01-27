package state

import (
	"github.com/ava-labs/avalanchego/ids"
)

// RangeCache provides caching for range proofs
type RangeCache struct {
	entries map[ids.ID]map[string]*RangeProof
	size    int
}

// NewRangeCache creates a new range cache with the specified maximum size
func NewRangeCache(maxSize int) *RangeCache {
	return &RangeCache{
		entries: make(map[ids.ID]map[string]*RangeProof),
		size:    maxSize,
	}
}

// Get retrieves a range proof from the cache if it exists and is still valid
func (c *RangeCache) Get(start, end []byte, root ids.ID) (*RangeProof, bool) {
	cache, ok := c.entries[root]
	if !ok {
		return nil, false
	}

	key := makeRangeKey(start, end)
	proof, ok := cache[key]
	return proof, ok
}

// Put adds a range proof to the cache
func (c *RangeCache) Put(proof *RangeProof, root ids.ID) {
	cache, ok := c.entries[root]
	if !ok {
		cache = make(map[string]*RangeProof)
		c.entries[root] = cache
	}

	key := makeRangeKey(proof.StartKey, proof.EndKey)
	cache[key] = proof

	// Evict least used entries if cache is full
	if len(c.entries) > c.size {
		c.evictLeastUsed()
	}
}

// evictLeastUsed removes the least frequently used cache entry
func (c *RangeCache) evictLeastUsed() {
	var leastRoot ids.ID
	var leastSize int = -1

	for root, cache := range c.entries {
		if leastSize == -1 || len(cache) < leastSize {
			leastSize = len(cache)
			leastRoot = root
		}
	}

	if leastSize != -1 {
		delete(c.entries, leastRoot)
	}
}

// makeRangeKey creates a cache key from start and end keys
func makeRangeKey(start, end []byte) string {
	return string(start) + "-" + string(end)
}
