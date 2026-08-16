package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// cacheMu guards all cache operations (in-memory + disk).
var cacheMu sync.RWMutex

// memCache is the in-memory copy of the dashboard cache.
// Reads come from memory; writes go to both memory and disk.
var memCache *DashboardCache

// ServerCache holds the last-known state for one server.
type ServerCache struct {
	AppCount        int `json:"app_count"`
	RunningApps     int `json:"running_apps"`
	ServiceCount    int `json:"service_count"`
	RunningServices int `json:"running_services"`
	// UntrackedApps counts app containers running on the server with no entry
	// in /etc/neo/state.json. Non-zero means the dashboard counts are an
	// undercount — the number people notice as "it says 0 but we deployed".
	UntrackedApps int       `json:"untracked_apps,omitempty"`
	Reachable     bool      `json:"reachable"`
	Error         string    `json:"error,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// DashboardCache is the full cache file — one entry per server name.
// Written after every successful SSH fetch; read on startup to avoid blocking on SSH.
type DashboardCache struct {
	Servers map[string]ServerCache `json:"servers"`
}

// Get returns the cache entry for a server, or nil if not present.
func (c *DashboardCache) Get(serverName string) *ServerCache {
	if c.Servers == nil {
		return nil
	}
	sc, ok := c.Servers[serverName]
	if !ok {
		return nil
	}
	return &sc
}

// Set updates the cache entry for a server.
func (c *DashboardCache) Set(serverName string, sc ServerCache) {
	if c.Servers == nil {
		c.Servers = make(map[string]ServerCache)
	}
	c.Servers[serverName] = sc
}

// CachePath returns the dashboard cache file path (~/.neo/cache.json).
func CachePath() string {
	return filepath.Join(Dir(), "cache.json")
}

// clone returns a deep copy so callers can read the cache without holding the
// lock while background refreshes keep writing to the shared map.
func (c *DashboardCache) clone() *DashboardCache {
	out := &DashboardCache{Servers: make(map[string]ServerCache, len(c.Servers))}
	for name, sc := range c.Servers {
		out.Servers[name] = sc
	}
	return out
}

// LoadCache returns a snapshot of the dashboard cache.
// On first call, loads from disk and caches in memory.
//
// The returned value is a copy, never the shared map: the dashboard renders
// from it on the main goroutine while up to ten background goroutines refresh
// server entries, and handing out the live map crashed the process with
// "concurrent map read and map write".
func LoadCache() *DashboardCache {
	cacheMu.RLock()
	if memCache != nil {
		snapshot := memCache.clone()
		cacheMu.RUnlock()
		return snapshot
	}
	cacheMu.RUnlock()

	// First call — load from disk and populate memory cache
	cacheMu.Lock()
	defer cacheMu.Unlock()

	// Double-check after acquiring write lock
	if memCache != nil {
		return memCache.clone()
	}

	data, err := os.ReadFile(CachePath())
	if err != nil {
		return nil
	}
	var c DashboardCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	memCache = &c
	return memCache.clone()
}

// GetServerCache returns a copy of one server's cache entry, or nil if the
// server has never been fetched. Prefer this over LoadCache().Get for single
// lookups — it copies one entry instead of the whole map.
func GetServerCache(serverName string) *ServerCache {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	if memCache == nil {
		return nil
	}
	sc, ok := memCache.Servers[serverName]
	if !ok {
		return nil
	}
	return &sc
}

// SaveCache writes the dashboard cache to both memory and disk.
// Safe to call from multiple goroutines.
func SaveCache(c *DashboardCache) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	saveCacheLocked(c)
}

// saveCacheLocked writes cache to memory and disk. Caller must hold cacheMu.
func saveCacheLocked(c *DashboardCache) {
	memCache = c

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	os.MkdirAll(Dir(), 0o700)
	// Atomic write: temp file + rename to prevent corruption on crash
	tmpPath := CachePath() + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return
	}
	os.Rename(tmpPath, CachePath()) //nolint:errcheck
}

// UpdateServerCache atomically updates one server entry in both memory and disk.
// Safe to call from multiple goroutines.
func UpdateServerCache(serverName string, sc ServerCache) {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	c := memCache
	if c == nil {
		// Try loading from disk
		data, err := os.ReadFile(CachePath())
		if err == nil {
			var loaded DashboardCache
			if json.Unmarshal(data, &loaded) == nil {
				c = &loaded
			}
		}
	}
	if c == nil {
		c = &DashboardCache{Servers: make(map[string]ServerCache)}
	}
	c.Set(serverName, sc)
	saveCacheLocked(c)
}
