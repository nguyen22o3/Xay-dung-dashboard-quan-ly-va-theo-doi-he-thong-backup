package main

import (
	"sync"
	"time"
)

// ===== CACHE NẰM TRONG RAM CHO CÁC API ĐỌC (Giảm tải SSH & Chống nghẽn) =====

type cacheEntry struct {
	value     []byte
	expiresAt time.Time
}

type flightCall struct {
	done chan struct{}
	val  []byte
	err  error
}

var (
	cacheMu  sync.Mutex
	cache    = map[string]cacheEntry{}
	inFlight = map[string]*flightCall{}
)

func cachedGet(key string, ttl time.Duration, producer func() ([]byte, error)) ([]byte, error) {
	return cachedGetWithPolicy(key, ttl, producer, nil, nil)
}

func cachedGetWithPolicy(key string, ttl time.Duration, producer func() ([]byte, error), ttlForValue func([]byte) time.Duration, staleValue func([]byte) []byte) ([]byte, error) {
	return cachedRead(key, ttl, producer, ttlForValue, staleValue, true)
}

func cachedGetFreshOrError(key string, ttl time.Duration, producer func() ([]byte, error)) ([]byte, error) {
	return cachedRead(key, ttl, producer, nil, nil, false)
}

func cachedRead(key string, ttl time.Duration, producer func() ([]byte, error), ttlForValue func([]byte) time.Duration, staleValue func([]byte) []byte, allowStale bool) ([]byte, error) {
	cacheMu.Lock()
	entry := cache[key]
	if time.Now().Before(entry.expiresAt) {
		cacheMu.Unlock()
		return entry.value, nil
	}
	call, shared := inFlight[key]
	if !shared {
		call = &flightCall{done: make(chan struct{})}
		inFlight[key] = call
	}
	cacheMu.Unlock()

	serveStale := allowStale && len(entry.value) > 0
	if !shared {
		load := func() {
			value, err := producer()
			cacheMu.Lock()
			defer cacheMu.Unlock()
			if inFlight[key] == call {
				if err == nil {
					if ttlForValue != nil {
						ttl = ttlForValue(value)
					}
					cache[key] = cacheEntry{value: value, expiresAt: time.Now().Add(ttl)}
				}
				delete(inFlight, key)
			}
			call.val, call.err = value, err
			close(call.done)
		}
		if serveStale {
			go load()
		} else {
			load()
		}
	}
	if serveStale {
		if staleValue != nil {
			if marked := staleValue(entry.value); len(marked) > 0 {
				return marked, nil
			}
		}
		return entry.value, nil
	}
	<-call.done
	return call.val, call.err
}

func invalidate(keys ...string) {
	// Keep callers independent of the shared internal read-cache layout.
	for _, key := range keys {
		switch key {
		case "backup-status":
			keys = append(keys, "drive-inventory", "backup-activity")
		case "recovery-snapshots":
			keys = append(keys, "drive-inventory")
		case "local-snapshots":
			keys = append(keys, "server-status")
		}
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	for _, k := range keys {
		delete(cache, k)
		// Old reads may finish later, but cannot refill the new configuration's cache.
		delete(inFlight, k)
	}
}

func refreshReadCaches() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	for key, entry := range cache {
		entry.expiresAt = time.Time{}
		cache[key] = entry
	}
	// Detach prior producers so they cannot make pre-refresh values fresh again.
	clear(inFlight)
}
