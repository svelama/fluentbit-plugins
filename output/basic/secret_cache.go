package main

import (
	"sync"
	"time"
)

// SecretCache implements a thread-safe cache for Kubernetes secrets with TTL
type SecretCache struct {
	cache map[string]*cacheEntry
	ttl   time.Duration
	mu    sync.RWMutex
}

// cacheEntry represents a cached secret value with expiration time
type cacheEntry struct {
	value      string
	expiration time.Time
}

// NewSecretCache creates a new SecretCache with the specified TTL
func NewSecretCache(ttl time.Duration) *SecretCache {
	cache := &SecretCache{
		cache: make(map[string]*cacheEntry),
		ttl:   ttl,
	}

	// Start background cleanup goroutine to remove expired entries
	go cache.cleanupExpired()

	return cache
}

// Get retrieves a value from the cache if it exists and hasn't expired
// Returns the value and true if found and valid, empty string and false otherwise
func (c *SecretCache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.cache[key]
	if !exists {
		return "", false
	}

	// Check if the entry has expired
	if time.Now().After(entry.expiration) {
		return "", false
	}

	return entry.value, true
}

// Set stores a value in the cache with the configured TTL
func (c *SecretCache) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[key] = &cacheEntry{
		value:      value,
		expiration: time.Now().Add(c.ttl),
	}
}

// Delete removes a value from the cache
func (c *SecretCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.cache, key)
}

// Clear removes all entries from the cache
func (c *SecretCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[string]*cacheEntry)
}

// Size returns the current number of entries in the cache (including expired ones)
func (c *SecretCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.cache)
}

// cleanupExpired periodically removes expired entries from the cache
// This prevents the cache from growing indefinitely with stale entries
func (c *SecretCache) cleanupExpired() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for key, entry := range c.cache {
			if now.After(entry.expiration) {
				delete(c.cache, key)
			}
		}
		c.mu.Unlock()
	}
}
