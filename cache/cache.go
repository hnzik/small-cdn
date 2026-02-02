package cache

type Stats struct {
	Hits       uint64
	Misses     uint64
	Evictions  uint64
	Entries    int
	SizeBytes  int64
	MaxBytes   int64
}

type Cache interface {
	Get(key string) (*Entry, bool)
	Set(key string, entry *Entry)
	Delete(key string)
	Clear()
	Stats() Stats
}
