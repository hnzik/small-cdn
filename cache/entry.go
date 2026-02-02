package cache

import (
	"bytes"
	"encoding/gob"
	"net/http"
	"time"
)

type Entry struct {
	Body        []byte
	Headers     http.Header
	StatusCode  int
	ContentType string
	CachedAt    time.Time
	TTL         time.Duration
}

func (e *Entry) IsExpired() bool {
	return time.Since(e.CachedAt) > e.TTL
}

func (e *Entry) Age() time.Duration {
	return time.Since(e.CachedAt)
}

func (e *Entry) Size() int {
	size := len(e.Body)
	for k, v := range e.Headers {
		size += len(k)
		for _, val := range v {
			size += len(val)
		}
	}
	return size
}

func SerializeEntry(e *Entry) []byte {
	var buf bytes.Buffer
	gob.NewEncoder(&buf).Encode(e)
	return buf.Bytes()
}

func DeserializeEntry(data []byte) (*Entry, error) {
	var e Entry
	err := gob.NewDecoder(bytes.NewReader(data)).Decode(&e)
	return &e, err
}
