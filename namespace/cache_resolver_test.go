package namespace

import (
	"testing"
	"time"
)

func TestEntriesCacheAddExistingEntry(t *testing.T) {
	ec := newEntriesCache(time.Millisecond*2, 2)
	fstEntries := NewEntries()
	sndEntries := NewEntries()
	trdEntries := NewEntries()
	ec.store("example.com/foo/bar", fstEntries)
	time.Sleep(time.Microsecond * 500)
	ec.store("example.com/foo/app", sndEntries)
	time.Sleep(time.Microsecond * 500)
	ec.store("example.com/foo/bar", trdEntries)

	if ec.lookup("example.com/foo/app") != sndEntries {
		t.Errorf("Expected 2nd entries to be present.")
	}
	if ec.lookup("example.com/foo/bar") != trdEntries {
		t.Errorf("Expected 3rd entries to be present.")
	}

	time.Sleep(time.Microsecond * 1050)
	if ec.lookup("example.com/foo/app") != sndEntries {
		t.Errorf("Expected 2nd entries to be present.")
	}
	if ec.lookup("example.com/foo/bar") != trdEntries {
		t.Errorf("Expected 3rd entries to be present.")
	}

	time.Sleep(time.Microsecond * 500)
	if ec.lookup("example.com/foo/app") != nil {
		t.Errorf("Expected 2nd entries to be evicted.")
	}
	if ec.lookup("example.com/foo/bar") != trdEntries {
		t.Errorf("Expected 3rd entries to be present.")
	}

	time.Sleep(time.Microsecond * 500)
	if ec.lookup("example.com/foo/app") != nil {
		t.Errorf("Expected 2nd entries to be evicted.")
	}
	if ec.lookup("example.com/foo/bar") != nil {
		t.Errorf("Expected 3rd entries to be evicted.")
	}
}

func TestCacheResolverDefaultSettings(t *testing.T) {
	hr := NewHTTPResolver(&HTTPResolverConfig{Client: newMockHTTPClient()})
	cr := NewCacheResolver(hr, nil)

	assertHTTPResolve(t, cr, "example.com/library/bar", `
example.com			index	https://search.example.com/
example.com			pull	https://registry.example.com/v1/ version=1.0 trim
example.com			push	https://registry.example.com/v1/ version=1.0 trim
`, true)

	assertHTTPResolve(t, cr, "example.com/foo/app", `
example.com/foo		index	https://search.foo.com/
example.com/foo		pull	https://mirror.foo.com/v1/ version=1.0
example.com/foo		push	https://registry.foo.com/v1/ version=1.0
example.com/foo     namespace	example.com
`, true)

	// hit cache
	assertHTTPResolve(t, cr, "example.com/library/bar", `
example.com			index	https://search.example.com/
example.com			pull	https://registry.example.com/v1/ version=1.0 trim
example.com			push	https://registry.example.com/v1/ version=1.0 trim
`, false)

	assertHTTPResolve(t, cr, "example.com/foo/app", `
example.com/foo		index	https://search.foo.com/
example.com/foo		pull	https://mirror.foo.com/v1/ version=1.0
example.com/foo		push	https://registry.foo.com/v1/ version=1.0
example.com/foo     namespace	example.com
`, false)
}

func TestCacheResolverReachMaxCapacity(t *testing.T) {
	hr := NewHTTPResolver(&HTTPResolverConfig{Client: newMockHTTPClient()})
	cr := NewCacheResolver(hr, &CacheResolverConfig{MaxEntries: 2})

	assertHTTPResolve(t, cr, "example.com/project/main", `
example.com/project/main index https://search.project.com/
example.com/project/main pull https://mirror.project.com/v2/ version=2.0.1
example.com/project/main push https://registry-1.project.com/v2/ version=2.0.1
example.com/project/main namespace	example.com/project
`, true)

	// reach max capacity
	assertHTTPResolve(t, cr, "example.com/foo/app", `
example.com/foo		index	https://search.foo.com/
example.com/foo		pull	https://mirror.foo.com/v1/ version=1.0
example.com/foo		push	https://registry.foo.com/v1/ version=1.0
example.com/foo     namespace	example.com
`, true)

	// hit cache
	assertHTTPResolve(t, cr, "example.com/project/main", `
example.com/project/main index https://search.project.com/
example.com/project/main pull https://mirror.project.com/v2/ version=2.0.1
example.com/project/main push https://registry-1.project.com/v2/ version=2.0.1
example.com/project/main namespace	example.com/project
`, false)

	assertHTTPResolve(t, cr, "example.com/foo/app", `
example.com/foo		index	https://search.foo.com/
example.com/foo		pull	https://mirror.foo.com/v1/ version=1.0
example.com/foo		push	https://registry.foo.com/v1/ version=1.0
example.com/foo     namespace	example.com
`, false)

	// the first added entry gets removed
	assertHTTPResolve(t, cr, "example.com/library/bar", `
example.com			index	https://search.example.com/
example.com			pull	https://registry.example.com/v1/ version=1.0 trim
example.com			push	https://registry.example.com/v1/ version=1.0 trim
`, true)

	// this should still be in a cache
	assertHTTPResolve(t, cr, "example.com/foo/app", `
example.com/foo		index	https://search.foo.com/
example.com/foo		pull	https://mirror.foo.com/v1/ version=1.0
example.com/foo		push	https://registry.foo.com/v1/ version=1.0
example.com/foo     namespace	example.com
`, false)

	// no longer in cache
	assertHTTPResolve(t, cr, "example.com/project/main", `
example.com/project/main index https://search.project.com/
example.com/project/main pull https://mirror.project.com/v2/ version=2.0.1
example.com/project/main push https://registry-1.project.com/v2/ version=2.0.1
example.com/project/main namespace	example.com/project
`, true)

	// newest entry in cache
	assertHTTPResolve(t, cr, "example.com/library/bar", `
example.com			index	https://search.example.com/
example.com			pull	https://registry.example.com/v1/ version=1.0 trim
example.com			push	https://registry.example.com/v1/ version=1.0 trim
`, false)
}

func TestCacheResolverCollectsExpired(t *testing.T) {
	hr := NewHTTPResolver(&HTTPResolverConfig{Client: newMockHTTPClient()})
	cr := NewCacheResolver(hr, &CacheResolverConfig{ExpireAfter: time.Millisecond})

	assertHTTPResolve(t, cr, "example.com/project/main", `
example.com/project/main index https://search.project.com/
example.com/project/main pull https://mirror.project.com/v2/ version=2.0.1
example.com/project/main push https://registry-1.project.com/v2/ version=2.0.1
example.com/project/main namespace	example.com/project
`, true)

	// hit cache
	assertHTTPResolve(t, cr, "example.com/project/main", `
example.com/project/main index https://search.project.com/
example.com/project/main pull https://mirror.project.com/v2/ version=2.0.1
example.com/project/main push https://registry-1.project.com/v2/ version=2.0.1
example.com/project/main namespace	example.com/project
`, false)

	time.Sleep(time.Millisecond * 2)

	// entry gets garbage collected
	assertHTTPResolve(t, cr, "example.com/project/main", `
example.com/project/main index https://search.project.com/
example.com/project/main pull https://mirror.project.com/v2/ version=2.0.1
example.com/project/main push https://registry-1.project.com/v2/ version=2.0.1
example.com/project/main namespace	example.com/project
`, true)
}
