package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// hashCacheEntry records everything needed to reuse a file's SHA-1 without
// rehashing: if neither size nor modification time changed, the content is
// assumed unchanged.
type hashCacheEntry struct {
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"` // Unix nanoseconds
	SHA1    string `json:"sha1"`  // base64-encoded digest
}

// HashCache persists path -> (size, mtime, sha1) so repeated drops of the same
// folder skip hashing entirely. It is safe for concurrent use by upload
// workers. A missing or unreadable cache file degrades silently to no cache.
type HashCache struct {
	mu      sync.Mutex
	path    string
	entries map[string]hashCacheEntry
	dirty   bool
}

// HashCachePath returns the cache file location next to the config file, or
// an empty string when the config path is not known yet.
func HashCachePath() string {
	if ConfigPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(ConfigPath), "sha1.cache.json")
}

// NewHashCache loads (or starts an empty) cache persisted at path. An empty
// path creates an in-memory cache that is never saved.
func NewHashCache(path string) *HashCache {
	cache := &HashCache{path: path, entries: make(map[string]hashCacheEntry)}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var loaded map[string]hashCacheEntry
			if json.Unmarshal(data, &loaded) == nil && loaded != nil {
				cache.entries = loaded
			}
		}
	}
	return cache
}

// hashCacheKey normalizes a path into a stable map key: cleaned, symlink-free,
// and lowercased on case-insensitive filesystems.
func hashCacheKey(filePath string) string {
	key, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		key = filePath
	}
	key = filepath.Clean(key)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

// Get returns the cached SHA-1 for filePath when the file still has the same
// size and modification time as when it was hashed.
func (c *HashCache) Get(filePath string) ([]byte, bool) {
	info, err := os.Stat(filePath)
	if err != nil {
		c.remove(hashCacheKey(filePath))
		return nil, false
	}
	key := hashCacheKey(filePath)

	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	if !ok {
		return nil, false
	}
	if entry.Size != info.Size() || entry.ModTime != info.ModTime().UnixNano() {
		c.remove(key)
		return nil, false
	}
	sum, err := base64.StdEncoding.DecodeString(entry.SHA1)
	if err != nil {
		c.remove(key)
		return nil, false
	}
	return sum, true
}

// Put records the SHA-1 of filePath together with its current size and
// modification time. Stat failures leave the cache unchanged.
func (c *HashCache) Put(filePath string, sum []byte) {
	info, err := os.Stat(filePath)
	if err != nil {
		return
	}
	key := hashCacheKey(filePath)

	c.mu.Lock()
	c.entries[key] = hashCacheEntry{
		Size:    info.Size(),
		ModTime: info.ModTime().UnixNano(),
		SHA1:    base64.StdEncoding.EncodeToString(sum),
	}
	c.dirty = true
	c.mu.Unlock()
}

func (c *HashCache) remove(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.dirty = true
	c.mu.Unlock()
}

// Len returns the number of cached entries.
func (c *HashCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Save writes the cache to disk atomically when it changed since the last
// save. Errors are swallowed: the cache is an optimization, not state that
// must survive.
func (c *HashCache) Save() {
	c.mu.Lock()
	if !c.dirty || c.path == "" {
		c.mu.Unlock()
		return
	}
	entries := make(map[string]hashCacheEntry, len(c.entries))
	for key, entry := range c.entries {
		entries[key] = entry
	}
	path := c.path
	c.dirty = false
	c.mu.Unlock()

	data, err := json.Marshal(entries)
	if err != nil {
		return
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return
	}
	temporary, err := os.CreateTemp(directory, ".sha1-cache-*")
	if err != nil {
		return
	}
	_, writeErr := temporary.Write(data)
	closeErr := temporary.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(temporary.Name())
		return
	}
	_ = os.Rename(temporary.Name(), path)
}

// calculateSHA1Cached hashes filePath through the cache: cached entries skip
// the disk read entirely, freshly computed digests are stored back.
func calculateSHA1Cached(ctx context.Context, filePath string, cache *HashCache) ([]byte, error) {
	if cache != nil {
		if sum, ok := cache.Get(filePath); ok {
			return sum, nil
		}
	}
	sum, err := CalculateSHA1(ctx, filePath)
	if err != nil {
		return nil, err
	}
	if cache != nil {
		cache.Put(filePath, sum)
	}
	return sum, nil
}
