package cache

import (
	"github.com/docker/distribution/context"
	"github.com/docker/distribution/digest"

	"github.com/docker/distribution"
)

type CacheMetrics struct {
	Requests uint64
	Hits     uint64
	Misses   uint64
}

type CacheMetricsTracker interface {
	Hit()
	Miss()
	Metrics() CacheMetrics
}

type cachedBlobStatter struct {
	cache   distribution.BlobDescriptorService
	backend distribution.BlobStatter
	tracker CacheMetricsTracker
}

func NewCachedBlobStatter(cache distribution.BlobDescriptorService, backend distribution.BlobStatter) distribution.BlobStatter {
	return &cachedBlobStatter{
		cache:   cache,
		backend: backend,
	}
}

func NewCachedBlobStatterWithMetrics(cache distribution.BlobDescriptorService, backend distribution.BlobStatter, tracker CacheMetricsTracker) distribution.BlobStatter {
	return &cachedBlobStatter{
		cache:   cache,
		backend: backend,
		tracker: tracker,
	}
}

func (cbds *cachedBlobStatter) Stat(ctx context.Context, dgst digest.Digest) (distribution.Descriptor, error) {
	desc, err := cbds.cache.Stat(ctx, dgst)
	if err != nil {
		if err != distribution.ErrBlobUnknown {
			context.GetLogger(ctx).Errorf("error retrieving descriptor from cache: %v", err)
		}

		goto fallback
	}

	if cbds.tracker != nil {
		cbds.tracker.Hit()
	}
	return desc, nil
fallback:
	if cbds.tracker != nil {
		cbds.tracker.Miss()
	}
	desc, err = cbds.backend.Stat(ctx, dgst)
	if err != nil {
		return desc, err
	}

	if err := cbds.cache.SetDescriptor(ctx, dgst, desc); err != nil {
		context.GetLogger(ctx).Errorf("error adding descriptor %v to cache: %v", desc.Digest, err)
	}

	return desc, err
}
