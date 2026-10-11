package reader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"searchan/internal/fsutil"
	"searchan/internal/httpx"
)

// Cache holds page data on disk, not in a growing process-wide map. Fixed
// striped locks coalesce concurrent requests for the same cache key.
type Cache struct {
	Dir   string
	TTL   time.Duration
	Next  Reader
	once  sync.Once
	gates [32]chan struct{}
}

func (c *Cache) gate(hash byte) chan struct{} {
	c.once.Do(func() {
		for i := range c.gates {
			c.gates[i] = make(chan struct{}, 1)
		}
	})
	return c.gates[int(hash)%len(c.gates)]
}

// Prune scans fixed batches; TTL bounds retention without retaining an index.
// Concurrent refreshes take the same striped lock as Read.
func (c *Cache) Prune() error {
	f, err := os.Open(c.Dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	ttl := c.TTL
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	for {
		entries, err := f.ReadDir(64)
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || len(name) != 69 || !strings.HasSuffix(name, ".json") {
				continue
			}
			hash, decodeErr := hex.DecodeString(name[:64])
			if decodeErr != nil {
				continue
			}
			gate := c.gate(hash[0])
			gate <- struct{}{}
			path := filepath.Join(c.Dir, name)
			if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > ttl {
				_ = os.Remove(path)
			}
			<-gate
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (c *Cache) Read(ctx context.Context, raw string) (Document, error) {
	return c.read(ctx, raw, c.TTL)
}

// ReadFresh caps cache age at 30 seconds for explicitly recent questions.
// It shares the same locks and coalesces concurrent refreshes.
func (c *Cache) ReadFresh(ctx context.Context, raw string) (Document, error) {
	ttl := c.TTL
	if ttl <= 0 || ttl > 30*time.Second {
		ttl = 30 * time.Second
	}
	return c.read(ctx, raw, ttl)
}

func (c *Cache) read(ctx context.Context, raw string, ttl time.Duration) (Document, error) {
	// Extraction rules changed: legacy text may contain recommendation previews.
	// Namespace the disk key so the next read refreshes it without deleting data.
	hash := sha256.Sum256([]byte("page-text-v2\x00" + raw))
	key := hex.EncodeToString(hash[:])
	path := filepath.Join(c.Dir, key+".json")
	gate := c.gate(hash[0])
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return Document{}, ctx.Err()
	}
	if ctx.Err() != nil {
		return Document{}, ctx.Err()
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if f, err := os.Open(path); err == nil {
		var doc Document
		decodeErr := httpx.Decode(f, 128<<10, &doc)
		f.Close()
		age := time.Since(doc.FetchedAt)
		if decodeErr == nil && age >= 0 && age < ttl && CheckContent(doc.Title, doc.Text) == nil {
			return doc, nil
		}
	}
	doc, err := c.Next.Read(ctx, raw)
	if err != nil {
		return doc, err
	}
	if err := CheckContent(doc.Title, doc.Text); err != nil {
		return Document{}, err
	}
	// Failure to cache must not fail a successful page read.
	_ = fsutil.WriteJSONLimit(path, doc, 128<<10)
	return doc, nil
}
