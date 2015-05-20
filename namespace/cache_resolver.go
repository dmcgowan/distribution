package namespace

import (
	"container/list"
	"sync"
	"time"
)

const DefaultMaxCachedEntries = 512

type cacheEntry struct {
	name    string
	created time.Time
	entries *Entries
}

func newCacheEntry(name string, entries *Entries) cacheEntry {
	return cacheEntry{name, time.Now(), entries}
}

type entriesCache struct {
	mutex       sync.Mutex
	cache       map[string]cacheEntry
	expireAfter time.Duration
	maxEntries  int
	/* Contains pointers to cache entries sorted by the time of their addition.
	 * Entry added last will be at the end. */
	expirationQueue *list.List
}

func newEntriesCache(expireAfter time.Duration, maxEntries int) *entriesCache {
	var expirationQueue *list.List
	if maxEntries > 0 {
		expirationQueue = list.New()
	}
	return &entriesCache{
		cache:           make(map[string]cacheEntry),
		expireAfter:     expireAfter,
		maxEntries:      maxEntries,
		expirationQueue: expirationQueue,
	}
}

// Must only be called from lookup method.
func (sc *entriesCache) garbageCollectExpired() {
	if sc.expirationQueue == nil {
		return
	}
	now := time.Now()
	elem := sc.expirationQueue.Front()
	for elem != nil && elem.Value.(*cacheEntry).created.Add(sc.expireAfter).Before(now) {
		delete(sc.cache, elem.Value.(*cacheEntry).name)
		next := elem.Next()
		sc.expirationQueue.Remove(elem)
		elem = next
	}
}

func (sc *entriesCache) lookup(name string) *Entries {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()
	entry, exists := sc.cache[name]
	if exists {
		if sc.expireAfter == 0 || !entry.created.Add(sc.expireAfter).Before(time.Now()) {
			return entry.entries
		}
		sc.garbageCollectExpired()
	}
	if !exists {
	}
	return nil
}

func (sc *entriesCache) store(name string, entries *Entries) {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()
	if sc.maxEntries > 0 && len(sc.cache) >= sc.maxEntries {
		elem := sc.expirationQueue.Front()
		delete(sc.cache, elem.Value.(*cacheEntry).name)
		sc.expirationQueue.Remove(elem)
	}
	entry := newCacheEntry(name, entries)
	sc.cache[name] = entry
	sc.expirationQueue.PushBack(&entry)
}

type CacheResolverConfig struct {
	/* Time interval saying how long to keep entries in cache.
	 * 0 says undefinitely. */
	ExpireAfter time.Duration
	/* How many entries can be kept at most. If reached during inserting
	 * new one, the oldest entry will be removed. If 0, it will be set to
	 * DefaultMaxCachedEntries. If -1 no limit will be applied. */
	MaxEntries int
}

type cacheResolver struct {
	baseResolver Resolver
	cache        *entriesCache
}

func NewCacheResolver(baseResolver Resolver, config *CacheResolverConfig) Resolver {
	if config == nil {
		config = &CacheResolverConfig{ExpireAfter: time.Hour * 24}
	}
	if config.MaxEntries == 0 {
		config.MaxEntries = DefaultMaxCachedEntries
	}
	return &cacheResolver{baseResolver, newEntriesCache(config.ExpireAfter, config.MaxEntries)}
}

func (cr *cacheResolver) Resolve(name string) (*Entries, error) {
	entries := cr.cache.lookup(name)
	if entries != nil {
		return entries, nil
	}
	entries, err := cr.baseResolver.Resolve(name)
	if err != nil {
		return nil, err
	}
	cr.cache.store(name, entries)
	return entries, nil
}
