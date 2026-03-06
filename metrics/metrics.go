package metrics

import (
	"context"
	"log"
	"os"

	"github.com/go-logr/stdr"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var Tracer trace.Tracer

var (
	RequestsTotal        metric.Int64Counter
	CacheHits            metric.Int64Counter
	CacheMisses          metric.Int64Counter
	OriginTTFB           metric.Float64Histogram
	TransferTime         metric.Float64Histogram
	SingleFlightDeduped  metric.Int64Counter
	RateLimitRejected    metric.Int64Counter
	RateLimitWaitSeconds metric.Float64Histogram
	OriginRequestsTotal  metric.Int64Counter
)

type CacheStatsProvider interface {
	MemoryStats() CacheStats
	DiskStats() CacheStats
}

type CacheStats struct {
	Entries   int
	SizeBytes int64
}

func Attr(key, value string) attribute.KeyValue {
	return attribute.String(key, value)
}

func Init(ctx context.Context, nodeID string, cacheStats CacheStatsProvider) (shutdown func(context.Context) error, err error) {
	if os.Getenv("OTEL_DEBUG") != "" {
		stdr.SetVerbosity(5)
		otel.SetLogger(stdr.New(log.New(os.Stderr, "[otel] ", log.LstdFlags)))
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			log.Printf("[otel] error: %v", err)
		}))
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithAttributes(attribute.String("service.instance.id", nodeID)),
	)
	if err != nil {
		return nil, err
	}

	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
	)
	otel.SetMeterProvider(meterProvider)

	traceExporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExporter),
	)
	otel.SetTracerProvider(tracerProvider)
	Tracer = tracerProvider.Tracer("cdn")

	meter := meterProvider.Meter("cdn")

	if err := initInstruments(meter); err != nil {
		return nil, err
	}

	if err := registerCacheGauges(meter, cacheStats); err != nil {
		return nil, err
	}

	return func(ctx context.Context) error {
		if err := tracerProvider.Shutdown(ctx); err != nil {
			return err
		}
		return meterProvider.Shutdown(ctx)
	}, nil
}

func initInstruments(meter metric.Meter) error {
	var err error

	RequestsTotal, err = meter.Int64Counter("cdn_requests_total",
		metric.WithDescription("Total number of requests"))
	if err != nil {
		return err
	}

	CacheHits, err = meter.Int64Counter("cdn_cache_hits_total",
		metric.WithDescription("Total number of cache hits"))
	if err != nil {
		return err
	}

	CacheMisses, err = meter.Int64Counter("cdn_cache_misses_total",
		metric.WithDescription("Total number of cache misses"))
	if err != nil {
		return err
	}

	OriginTTFB, err = meter.Float64Histogram("cdn_origin_ttfb_seconds",
		metric.WithDescription("Time to first byte from origin"),
		metric.WithUnit("s"))
	if err != nil {
		return err
	}

	TransferTime, err = meter.Float64Histogram("cdn_transfer_time_seconds",
		metric.WithDescription("Total transfer time including body"),
		metric.WithUnit("s"))
	if err != nil {
		return err
	}

	SingleFlightDeduped, err = meter.Int64Counter("cdn_singleflight_deduped_total",
		metric.WithDescription("Requests served from shared single-flight response"))
	if err != nil {
		return err
	}

	RateLimitRejected, err = meter.Int64Counter("cdn_ratelimit_rejected_total",
		metric.WithDescription("Requests rejected due to origin rate limiting"))
	if err != nil {
		return err
	}

	RateLimitWaitSeconds, err = meter.Float64Histogram("cdn_ratelimit_wait_seconds",
		metric.WithDescription("Time spent waiting for rate limiter"),
		metric.WithUnit("s"))
	if err != nil {
		return err
	}

	OriginRequestsTotal, err = meter.Int64Counter("cdn_origin_requests_total",
		metric.WithDescription("Total requests sent to origin after deduplication"))
	if err != nil {
		return err
	}

	return nil
}

func registerCacheGauges(meter metric.Meter, stats CacheStatsProvider) error {
	if stats == nil {
		return nil
	}

	_, err := meter.Float64ObservableGauge("cdn_memory_cache_size_bytes",
		metric.WithDescription("Current memory cache size in bytes"),
		metric.WithUnit("By"),
		metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
			o.Observe(float64(stats.MemoryStats().SizeBytes))
			return nil
		}),
	)
	if err != nil {
		return err
	}

	_, err = meter.Float64ObservableGauge("cdn_memory_cache_entries",
		metric.WithDescription("Current number of memory cache entries"),
		metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
			o.Observe(float64(stats.MemoryStats().Entries))
			return nil
		}),
	)
	if err != nil {
		return err
	}

	_, err = meter.Float64ObservableGauge("cdn_disk_cache_size_bytes",
		metric.WithDescription("Current disk cache size in bytes"),
		metric.WithUnit("By"),
		metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
			o.Observe(float64(stats.DiskStats().SizeBytes))
			return nil
		}),
	)
	if err != nil {
		return err
	}

	_, err = meter.Float64ObservableGauge("cdn_disk_cache_entries",
		metric.WithDescription("Current number of disk cache entries"),
		metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
			o.Observe(float64(stats.DiskStats().Entries))
			return nil
		}),
	)
	if err != nil {
		return err
	}

	return nil
}
