package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	entriesBucket = []byte("entries")
	lruBucket     = []byte("lru")
)

type DiskMetadata struct {
	Hash        string
	Headers     http.Header
	StatusCode  int
	ContentType string
	CachedAt    time.Time
	TTL         time.Duration
	Size        int64
	LastAccess  time.Time
}

type DiskCache struct {
	path      string
	maxBytes  int64
	sizeBytes int64
	db        *bolt.DB
	hits      uint64
	misses    uint64
}

func NewDiskCache(path string, maxSizeMB int) (*DiskCache, error) {
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	dbPath := filepath.Join(path, "metadata.db")
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open metadata db: %w", err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(entriesBucket); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(lruBucket); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create buckets: %w", err)
	}

	dc := &DiskCache{
		path:     path,
		maxBytes: int64(maxSizeMB) * 1024 * 1024,
		db:       db,
	}

	dc.calculateSize()
	return dc, nil
}

func (dc *DiskCache) calculateSize() {
	var total int64
	dc.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(entriesBucket)
		return b.ForEach(func(k, v []byte) error {
			var meta DiskMetadata
			if err := gob.NewDecoder(bytes.NewReader(v)).Decode(&meta); err == nil {
				total += meta.Size
			}
			return nil
		})
	})
	atomic.StoreInt64(&dc.sizeBytes, total)
}

func (dc *DiskCache) Get(key string) (*Entry, bool) {
	var meta DiskMetadata
	err := dc.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(entriesBucket)
		v := b.Get([]byte(key))
		if v == nil {
			return fmt.Errorf("not found")
		}
		return gob.NewDecoder(bytes.NewReader(v)).Decode(&meta)
	})

	if err != nil {
		atomic.AddUint64(&dc.misses, 1)
		return nil, false
	}

	if time.Since(meta.CachedAt) > meta.TTL {
		dc.Delete(key)
		atomic.AddUint64(&dc.misses, 1)
		return nil, false
	}

	dataPath := filepath.Join(dc.path, meta.Hash+".data")
	body, err := os.ReadFile(dataPath)
	if err != nil {
		dc.Delete(key)
		atomic.AddUint64(&dc.misses, 1)
		return nil, false
	}

	dc.updateLastAccess(key, meta)

	atomic.AddUint64(&dc.hits, 1)
	return &Entry{
		Body:        body,
		Headers:     meta.Headers,
		StatusCode:  meta.StatusCode,
		ContentType: meta.ContentType,
		CachedAt:    meta.CachedAt,
		TTL:         meta.TTL,
	}, true
}

func (dc *DiskCache) updateLastAccess(key string, meta DiskMetadata) {
	oldLRUKey := makeLRUKey(meta.LastAccess, key)
	meta.LastAccess = time.Now()
	newLRUKey := makeLRUKey(meta.LastAccess, key)

	dc.db.Update(func(tx *bolt.Tx) error {
		entries := tx.Bucket(entriesBucket)
		lru := tx.Bucket(lruBucket)

		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(&meta); err != nil {
			return err
		}
		if err := entries.Put([]byte(key), buf.Bytes()); err != nil {
			return err
		}

		lru.Delete(oldLRUKey)
		return lru.Put(newLRUKey, []byte(key))
	})
}

func (dc *DiskCache) Set(key string, entry *Entry) {
	hash := hashKey(key)
	dataPath := filepath.Join(dc.path, hash+".data")

	if err := os.WriteFile(dataPath, entry.Body, 0644); err != nil {
		return
	}

	meta := DiskMetadata{
		Hash:        hash,
		Headers:     entry.Headers,
		StatusCode:  entry.StatusCode,
		ContentType: entry.ContentType,
		CachedAt:    entry.CachedAt,
		TTL:         entry.TTL,
		Size:        int64(len(entry.Body)),
		LastAccess:  time.Now(),
	}

	dc.db.Update(func(tx *bolt.Tx) error {
		entries := tx.Bucket(entriesBucket)
		lru := tx.Bucket(lruBucket)

		oldValue := entries.Get([]byte(key))
		if oldValue != nil {
			var oldMeta DiskMetadata
			if gob.NewDecoder(bytes.NewReader(oldValue)).Decode(&oldMeta) == nil {
				atomic.AddInt64(&dc.sizeBytes, -oldMeta.Size)
				lru.Delete(makeLRUKey(oldMeta.LastAccess, key))
			}
		}

		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(&meta); err != nil {
			return err
		}
		if err := entries.Put([]byte(key), buf.Bytes()); err != nil {
			return err
		}
		if err := lru.Put(makeLRUKey(meta.LastAccess, key), []byte(key)); err != nil {
			return err
		}

		atomic.AddInt64(&dc.sizeBytes, meta.Size)
		return nil
	})

	dc.evictIfNeeded()
}

func (dc *DiskCache) Delete(key string) {
	dc.db.Update(func(tx *bolt.Tx) error {
		entries := tx.Bucket(entriesBucket)
		lru := tx.Bucket(lruBucket)

		v := entries.Get([]byte(key))
		if v == nil {
			return nil
		}

		var meta DiskMetadata
		if err := gob.NewDecoder(bytes.NewReader(v)).Decode(&meta); err != nil {
			return err
		}

		dataPath := filepath.Join(dc.path, meta.Hash+".data")
		os.Remove(dataPath)

		atomic.AddInt64(&dc.sizeBytes, -meta.Size)
		lru.Delete(makeLRUKey(meta.LastAccess, key))
		return entries.Delete([]byte(key))
	})
}

func (dc *DiskCache) Clear() {
	dc.db.Update(func(tx *bolt.Tx) error {
		entries := tx.Bucket(entriesBucket)
		entries.ForEach(func(k, v []byte) error {
			var meta DiskMetadata
			if gob.NewDecoder(bytes.NewReader(v)).Decode(&meta) == nil {
				dataPath := filepath.Join(dc.path, meta.Hash+".data")
				os.Remove(dataPath)
			}
			return nil
		})

		tx.DeleteBucket(entriesBucket)
		tx.DeleteBucket(lruBucket)
		tx.CreateBucket(entriesBucket)
		tx.CreateBucket(lruBucket)
		return nil
	})
	atomic.StoreInt64(&dc.sizeBytes, 0)
}

func (dc *DiskCache) evictIfNeeded() {
	for atomic.LoadInt64(&dc.sizeBytes) > dc.maxBytes {
		var keyToDelete string
		dc.db.View(func(tx *bolt.Tx) error {
			lru := tx.Bucket(lruBucket)
			c := lru.Cursor()
			_, v := c.First()
			if v != nil {
				keyToDelete = string(v)
			}
			return nil
		})

		if keyToDelete == "" {
			break
		}
		dc.Delete(keyToDelete)
	}
}

func (dc *DiskCache) Stats() Stats {
	var entries int
	dc.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(entriesBucket)
		entries = b.Stats().KeyN
		return nil
	})

	return Stats{
		Hits:      atomic.LoadUint64(&dc.hits),
		Misses:    atomic.LoadUint64(&dc.misses),
		Entries:   entries,
		SizeBytes: atomic.LoadInt64(&dc.sizeBytes),
		MaxBytes:  dc.maxBytes,
	}
}

func (dc *DiskCache) Close() error {
	return dc.db.Close()
}

func hashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", h[:8])
}

func makeLRUKey(t time.Time, key string) []byte {
	buf := make([]byte, 8+len(key))
	binary.BigEndian.PutUint64(buf[:8], uint64(t.UnixNano()))
	copy(buf[8:], key)
	return buf
}
