package core

import (
	"context"
	"crypto/sha1"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, directory, name, contents string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return path
}

func TestHashCacheHitAfterReopen(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "sha1.cache.json")

	file := writeTestFile(t, t.TempDir(), "photo.jpg", "hello world")

	first := NewHashCache(cachePath)
	sum, err := calculateSHA1Cached(context.Background(), file, first)
	if err != nil {
		t.Fatalf("calculate: %v", err)
	}
	first.Save()
	if first.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", first.Len())
	}

	want := sha1.Sum([]byte("hello world"))
	if string(sum) != string(want[:]) {
		t.Fatalf("unexpected digest")
	}

	// A reopened cache must serve the digest without touching the file again.
	reopened := NewHashCache(cachePath)
	cached, ok := reopened.Get(file)
	if !ok {
		t.Fatal("expected cache hit after reopen")
	}
	if string(cached) != string(want[:]) {
		t.Fatal("cached digest mismatch")
	}
}

func TestHashCacheInvalidatesOnChange(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "sha1.cache.json")
	file := writeTestFile(t, t.TempDir(), "photo.jpg", "original")

	cache := NewHashCache(cachePath)
	if _, err := calculateSHA1Cached(context.Background(), file, cache); err != nil {
		t.Fatalf("calculate: %v", err)
	}

	if err := os.WriteFile(file, []byte("changed"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	// Force a different mtime even on coarse filesystems.
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(file, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if _, ok := cache.Get(file); ok {
		t.Fatal("expected cache miss after content change")
	}
}

func TestHashCacheMissingFile(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "sha1.cache.json")
	file := writeTestFile(t, t.TempDir(), "gone.jpg", "data")

	cache := NewHashCache(cachePath)
	if _, err := calculateSHA1Cached(context.Background(), file, cache); err != nil {
		t.Fatalf("calculate: %v", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := cache.Get(file); ok {
		t.Fatal("expected miss for missing file")
	}
}
