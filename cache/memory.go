package cache

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/allegro/bigcache/v3"
)

type MemoryCache struct {
	cache  *bigcache.BigCache
	hits   uint64
	misses uint64
}

func NewMemoryCache(maxSizeMB int) (*MemoryCache, error) {
	config := bigcache.Config{
		Shards:           1024,
		LifeWindow:       24 * time.Hour,
		CleanWindow:      5 * time.Minute,
		MaxEntrySize:     10 * 1024 * 1024,
		HardMaxCacheSize: maxSizeMB,
		Verbose:          false,
	}

	cache, err := bigcache.New(context.Background(), config)
	if err != nil {
		return nil, err
	}

	return &MemoryCache{cache: cache}, nil
}

func (c *MemoryCache) Get(key string) (*Entry, bool) {
	data, err := c.cache.Get(key)
	if err != nil {
		atomic.AddUint64(&c.misses, 1)
		return nil, false
	}

	entry, err := DeserializeEntry(data)
	if err != nil {
		c.cache.Delete(key)
		atomic.AddUint64(&c.misses, 1)
		return nil, false
	}

	if entry.IsExpired() {
		c.cache.Delete(key)
		atomic.AddUint64(&c.misses, 1)
		return nil, false
	}

	atomic.AddUint64(&c.hits, 1)
	return entry, true
}

func (c *MemoryCache) Set(key string, entry *Entry) {
	data := SerializeEntry(entry)
	c.cache.Set(key, data)
}

func (c *MemoryCache) Delete(key string) {
	c.cache.Delete(key)
}

func (c *MemoryCache) Clear() {
	c.cache.Reset()
}

func (c *MemoryCache) Stats() Stats {
	return Stats{
		Hits:      atomic.LoadUint64(&c.hits),
		Misses:    atomic.LoadUint64(&c.misses),
		Entries:   c.cache.Len(),
		SizeBytes: int64(c.cache.Capacity()),
		MaxBytes:  int64(c.cache.Capacity()),
	}
}
