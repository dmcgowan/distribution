package namespace

import (
	"testing"
	"time"
)

func TestCacheResolverDefaultSettings(t *testing.T) {
	config := HTTPResolverConfig{
		Client:            newMockHTTPClient(),
		NSResolveCallback: func(string, scope) NSResolveActionEnum { return NSResolveActionIgnore },
	}
	hr := NewHTTPResolver(&config)
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
`, false)
}

func TestCacheResolverReachMaxCapacity(t *testing.T) {
	config := HTTPResolverConfig{
		Client:            newMockHTTPClient(),
		NSResolveCallback: func(string, scope) NSResolveActionEnum { return NSResolveActionIgnore },
	}
	hr := NewHTTPResolver(&config)
	cr := NewCacheResolver(hr, &CacheResolverConfig{MaxEntries: 2})

	assertHTTPResolve(t, cr, "example.com/project/main", `
example.com/project/main index https://search.project.com/
example.com/project/main pull https://mirror.project.com/v2/ version=2.0.1
example.com/project/main push https://registry-1.project.com/v2/ version=2.0.1
`, true)

	// reach max capacity
	assertHTTPResolve(t, cr, "example.com/foo/app", `
example.com/foo		index	https://search.foo.com/
example.com/foo		pull	https://mirror.foo.com/v1/ version=1.0
example.com/foo		push	https://registry.foo.com/v1/ version=1.0
`, true)

	// hit cache
	assertHTTPResolve(t, cr, "example.com/project/main", `
example.com/project/main index https://search.project.com/
example.com/project/main pull https://mirror.project.com/v2/ version=2.0.1
example.com/project/main push https://registry-1.project.com/v2/ version=2.0.1
`, false)

	assertHTTPResolve(t, cr, "example.com/foo/app", `
example.com/foo		index	https://search.foo.com/
example.com/foo		pull	https://mirror.foo.com/v1/ version=1.0
example.com/foo		push	https://registry.foo.com/v1/ version=1.0
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
`, false)

	// no longer in cache
	assertHTTPResolve(t, cr, "example.com/project/main", `
example.com/project/main index https://search.project.com/
example.com/project/main pull https://mirror.project.com/v2/ version=2.0.1
example.com/project/main push https://registry-1.project.com/v2/ version=2.0.1
`, true)

	// newest entry in cache
	assertHTTPResolve(t, cr, "example.com/library/bar", `
example.com			index	https://search.example.com/
example.com			pull	https://registry.example.com/v1/ version=1.0 trim
example.com			push	https://registry.example.com/v1/ version=1.0 trim
`, false)
}

func TestCacheResolverCollectsExpired(t *testing.T) {
	config := HTTPResolverConfig{
		Client:            newMockHTTPClient(),
		NSResolveCallback: func(string, scope) NSResolveActionEnum { return NSResolveActionIgnore },
	}
	hr := NewHTTPResolver(&config)
	cr := NewCacheResolver(hr, &CacheResolverConfig{ExpireAfter: time.Millisecond})

	assertHTTPResolve(t, cr, "example.com/project/main", `
example.com/project/main index https://search.project.com/
example.com/project/main pull https://mirror.project.com/v2/ version=2.0.1
example.com/project/main push https://registry-1.project.com/v2/ version=2.0.1
`, true)

	// hit cache
	assertHTTPResolve(t, cr, "example.com/project/main", `
example.com/project/main index https://search.project.com/
example.com/project/main pull https://mirror.project.com/v2/ version=2.0.1
example.com/project/main push https://registry-1.project.com/v2/ version=2.0.1
`, false)

	time.Sleep(time.Millisecond * 2)

	// entry gets garbage collected
	assertHTTPResolve(t, cr, "example.com/project/main", `
example.com/project/main index https://search.project.com/
example.com/project/main pull https://mirror.project.com/v2/ version=2.0.1
example.com/project/main push https://registry-1.project.com/v2/ version=2.0.1
`, true)
}
