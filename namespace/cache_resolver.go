package namespace

import (
	"container/list"
	"sync"
	"time"
)

const DefaultExpireAfter = time.Hour * 24
const DefaultCacheSize = 512

/* Cache interface for cacheResolver. */
type EntriesCache interface {
	Lookup(name string) *Entries
	Store(name string, entries *Entries)
}

type cacheEntry struct {
	name    string
	created time.Time
	entries *Entries
}

func newCacheEntry(name string, entries *Entries) cacheEntry {
	return cacheEntry{name, time.Now(), entries}
}

/* Thread-safe implementation of EntriesCache. It removes oldest entries when
 * they expire or a cache size is reached. Removal is done during both
 * `Lookup()` and `Store()` methods. */
type ExpiringEntriesCache struct {
	mutex       sync.Mutex
	cache       map[string]cacheEntry
	expireAfter time.Duration
	size        int
	/* Contains pointers to cache entries sorted by the time of their addition.
	 * Entry added last will be at the end. */
	expirationQueue *list.List
}

/* expireAfter is a time interval saying how long to keep entries in cache.
 * 0 means undefinitely.
 * If size is reached, the oldest entry will be removed before inserting
 * a new one. */
func NewExpiringEntriesCache(expireAfter time.Duration, size int) *ExpiringEntriesCache {
	var expirationQueue *list.List
	if size > 0 {
		expirationQueue = list.New()
	}
	return &ExpiringEntriesCache{
		cache:           make(map[string]cacheEntry),
		expireAfter:     expireAfter,
		size:            size,
		expirationQueue: expirationQueue,
	}
}

// Must only be called from inside of Lookup/Store methods.
func (sc *ExpiringEntriesCache) garbageCollectExpired() {
	if sc.expirationQueue == nil || sc.expireAfter == 0 {
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

func (sc *ExpiringEntriesCache) Lookup(name string) *Entries {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()
	sc.garbageCollectExpired()
	entry, exists := sc.cache[name]
	if exists {
		return entry.entries
	}
	return nil
}

func (sc *ExpiringEntriesCache) Store(name string, entries *Entries) {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()
	sc.garbageCollectExpired()
	// this shouldn't occur when used from cacheResolver
	if entry, exists := sc.cache[name]; exists {
		delete(sc.cache, name)
		elem := sc.expirationQueue.Front()
		for elem != nil && (elem.Value.(*cacheEntry).created.Before(entry.created) || elem.Value.(*cacheEntry).name != entry.name) {
			elem = elem.Next()
		}
		if elem != nil {
			sc.expirationQueue.Remove(elem)
		}
	}
	if sc.size > 0 && len(sc.cache) >= sc.size {
		elem := sc.expirationQueue.Front()
		delete(sc.cache, elem.Value.(*cacheEntry).name)
		sc.expirationQueue.Remove(elem)
	}
	entry := newCacheEntry(name, entries)
	sc.cache[name] = entry
	sc.expirationQueue.PushBack(&entry)
}

/* Generic caching resolver that stores results of prior resolutions and
 * returns them on subsequent calls. */
type cacheResolver struct {
	baseResolver Resolver
	cache        EntriesCache
}

/* Make a new cache provider with particular cache implementation.
 * If cache is nil, new ExpiringEntriesCache will be instantiated with
 * default parameters.
 */
func NewCacheResolver(baseResolver Resolver, cache EntriesCache) Resolver {
	if cache == nil {
		cache = NewExpiringEntriesCache(DefaultExpireAfter, DefaultCacheSize)
	}
	return &cacheResolver{baseResolver, cache}
}

func (cr *cacheResolver) Resolve(name string) (*Entries, error) {
	entries := cr.cache.Lookup(name)
	if entries != nil {
		return entries, nil
	}
	entries, err := cr.baseResolver.Resolve(name)
	if err != nil {
		return nil, err
	}
	cr.cache.Store(name, entries)
	return entries, nil
}
