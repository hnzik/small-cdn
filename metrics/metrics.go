package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cdn_requests_total",
			Help: "Total number of requests",
		},
		[]string{"status", "cache_status", "cache_tier", "content_type"},
	)

	CacheHits = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cdn_cache_hits_total",
			Help: "Total number of cache hits",
		},
		[]string{"tier"},
	)

	CacheMisses = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "cdn_cache_misses_total",
			Help: "Total number of cache misses",
		},
	)

	OriginTTFB = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "cdn_origin_ttfb_seconds",
			Help:    "Time to first byte from origin",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15),
		},
	)

	TransferTime = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cdn_transfer_time_seconds",
			Help:    "Total transfer time including body",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15),
		},
		[]string{"tier"},
	)

	MemoryCacheSizeBytes = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cdn_memory_cache_size_bytes",
			Help: "Current memory cache size in bytes",
		},
	)

	MemoryCacheEntries = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cdn_memory_cache_entries",
			Help: "Current number of memory cache entries",
		},
	)

	DiskCacheSizeBytes = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cdn_disk_cache_size_bytes",
			Help: "Current disk cache size in bytes",
		},
	)

	DiskCacheEntries = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cdn_disk_cache_entries",
			Help: "Current number of disk cache entries",
		},
	)

	DiskReadSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "cdn_disk_read_seconds",
			Help:    "Disk read latency",
			Buckets: prometheus.ExponentialBuckets(0.0001, 2, 12),
		},
	)

	SingleFlightDeduped = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "cdn_singleflight_deduped_total",
			Help: "Requests served from shared single-flight response",
		},
	)

	RateLimitRejected = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "cdn_ratelimit_rejected_total",
			Help: "Requests rejected due to origin rate limiting",
		},
	)

	RateLimitWaitSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "cdn_ratelimit_wait_seconds",
			Help:    "Time spent waiting for rate limiter",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 12),
		},
	)

	OriginRequestsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "cdn_origin_requests_total",
			Help: "Total requests sent to origin after deduplication",
		},
	)
)
