package cache

type CacheTier string

const (
	TierMemory CacheTier = "memory"
	TierDisk   CacheTier = "disk"
	TierNone   CacheTier = ""
)

type TieredResult struct {
	Entry *Entry
	Tier  CacheTier
	Found bool
}

type TieredCache struct {
	memory *MemoryCache
	disk   *DiskCache
}

func NewTieredCache(memory *MemoryCache, disk *DiskCache) *TieredCache {
	return &TieredCache{
		memory: memory,
		disk:   disk,
	}
}

func (tc *TieredCache) Get(key string) TieredResult {
	if entry, found := tc.memory.Get(key); found {
		return TieredResult{Entry: entry, Tier: TierMemory, Found: true}
	}

	if entry, found := tc.disk.Get(key); found {
		tc.memory.Set(key, entry)
		return TieredResult{Entry: entry, Tier: TierDisk, Found: true}
	}

	return TieredResult{Found: false}
}

func (tc *TieredCache) Set(key string, entry *Entry) {
	tc.disk.Set(key, entry)
	tc.memory.Set(key, entry)
}

func (tc *TieredCache) Delete(key string) {
	tc.memory.Delete(key)
	tc.disk.Delete(key)
}

func (tc *TieredCache) Clear() {
	tc.memory.Clear()
	tc.disk.Clear()
}

func (tc *TieredCache) MemoryStats() Stats {
	return tc.memory.Stats()
}

func (tc *TieredCache) DiskStats() Stats {
	return tc.disk.Stats()
}

func (tc *TieredCache) Close() error {
	return tc.disk.Close()
}
