package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"small-cdn/cache"
	"small-cdn/config"
	"small-cdn/metrics"
	"small-cdn/origin"
)

type Proxy struct {
	originURL  *url.URL
	cache      *cache.TieredCache
	config     *config.Config
	client     *http.Client
	protection *origin.Protection
}

func New(cfg *config.Config, c *cache.TieredCache) (*Proxy, error) {
	parsedURL, err := url.Parse(cfg.Origin.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid origin URL: %w", err)
	}

	protection := origin.NewProtection(origin.ProtectionConfig{
		SingleFlightEnabled: cfg.Origin.Protection.SingleFlight.Enabled,
		RateLimitEnabled:    cfg.Origin.Protection.RateLimit.Enabled,
		RateLimitRPS:        cfg.Origin.Protection.RateLimit.RPS,
		RateLimitBurst:      cfg.Origin.Protection.RateLimit.Burst,
		RateLimitTimeout:    cfg.Origin.Protection.RateLimit.Timeout,
	})

	return &Proxy{
		originURL: parsedURL,
		cache:     c,
		config:    cfg,
		client: &http.Client{
			Timeout: cfg.Origin.Timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		protection: protection,
	}, nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, span := metrics.Tracer.Start(r.Context(), r.Method+" "+r.URL.Path,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("http.request.method", r.Method),
			attribute.String("url.path", r.URL.Path),
		),
	)
	defer span.End()
	r = r.WithContext(ctx)

	start := time.Now()
	key := cacheKey(r)

	result := p.cache.Get(key)
	if result.Found {
		span.SetAttributes(attribute.String("cache.status", "HIT"), attribute.String("cache.tier", string(result.Tier)))
		p.serveFromCache(w, r, result, start)
		return
	}

	span.SetAttributes(attribute.String("cache.status", "MISS"))
	p.fetchFromOrigin(w, r, key, start)
}

func (p *Proxy) serveFromCache(w http.ResponseWriter, r *http.Request, result cache.TieredResult, start time.Time) {
	entry := result.Entry
	tier := result.Tier

	for k, v := range entry.Headers {
		w.Header()[k] = v
	}

	age := entry.Age()
	w.Header().Set("X-Cache", "HIT")
	w.Header().Set("X-Cache-Tier", string(tier))
	w.Header().Set("X-Cache-Age", strconv.FormatInt(int64(age.Seconds()), 10))
	w.Header().Set("X-Cache-TTL", strconv.FormatInt(int64(entry.TTL.Seconds()), 10))
	w.Header().Set("X-Node-Id", p.config.Server.NodeID)

	transferTime := time.Since(start)
	w.Header().Set("X-Transfer-Time-Ms", strconv.FormatInt(transferTime.Milliseconds(), 10))

	w.WriteHeader(entry.StatusCode)
	w.Write(entry.Body)

	ctx := r.Context()
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("http.response.status_code", entry.StatusCode))
	metrics.CacheHits.Add(ctx, 1, metric.WithAttributes(metrics.Attr("tier", string(tier))))
	metrics.TransferTime.Record(ctx, transferTime.Seconds(), metric.WithAttributes(metrics.Attr("tier", string(tier))))
	metrics.RequestsTotal.Add(ctx, 1, metric.WithAttributes(
		metrics.Attr("status", strconv.Itoa(entry.StatusCode)),
		metrics.Attr("cache_status", "HIT"),
		metrics.Attr("cache_tier", string(tier)),
		metrics.Attr("content_type", entry.ContentType),
	))
}

func (p *Proxy) fetchFromOrigin(w http.ResponseWriter, r *http.Request, key string, start time.Time) {
	waitStart := time.Now()
	useSingleFlight := canUseSingleFlight(r)

	ctx := r.Context()
	resp, shared, err := p.protection.Do(ctx, key, useSingleFlight, func() (*origin.Response, error) {
		metrics.OriginRequestsTotal.Add(ctx, 1)
		return p.doOriginRequest(r)
	})

	if p.config.Origin.Protection.RateLimit.Enabled {
		metrics.RateLimitWaitSeconds.Record(ctx, time.Since(waitStart).Seconds())
	}

	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		if errors.Is(err, origin.ErrRateLimited) {
			metrics.RateLimitRejected.Add(ctx, 1)
			http.Error(w, "origin rate limited", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "origin error", http.StatusBadGateway)
		return
	}

	if shared {
		metrics.SingleFlightDeduped.Add(ctx, 1)
	}

	p.serveOriginResponse(w, r, key, start, resp, shared)
}

func (p *Proxy) doOriginRequest(r *http.Request) (*origin.Response, error) {
	ctx, span := metrics.Tracer.Start(r.Context(), "cdn.origin.fetch",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("server.address", p.originURL.Host)),
	)
	defer span.End()

	reqURL := *p.originURL
	reqURL.Path = r.URL.Path
	reqURL.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(ctx, r.Method, reqURL.String(), r.Body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	for k, v := range r.Header {
		if k != "Host" {
			req.Header[k] = v
		}
	}

	ttfbStart := time.Now()
	resp, err := p.client.Do(req)
	ttfb := time.Since(ttfbStart)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	defer resp.Body.Close()

	span.SetAttributes(
		attribute.Int("http.status_code", resp.StatusCode),
		attribute.Float64("origin.ttfb_seconds", ttfb.Seconds()),
	)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	return &origin.Response{
		Body:        body,
		Headers:     cloneHeaders(resp.Header),
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		TTFB:        ttfb,
	}, nil
}

func (p *Proxy) serveOriginResponse(w http.ResponseWriter, r *http.Request, key string, start time.Time, resp *origin.Response, shared bool) {
	ttl := p.config.GetTTL(resp.ContentType)

	if r.Method == http.MethodGet && resp.StatusCode >= 200 && resp.StatusCode < 400 {
		entry := &cache.Entry{
			Body:        resp.Body,
			Headers:     resp.Headers,
			StatusCode:  resp.StatusCode,
			ContentType: resp.ContentType,
			CachedAt:    time.Now(),
			TTL:         ttl,
		}
		p.cache.Set(key, entry)
	}

	for k, v := range resp.Headers {
		w.Header()[k] = v
	}

	transferTime := time.Since(start)
	w.Header().Set("X-Cache", "MISS")
	w.Header().Set("X-Cache-TTL", strconv.FormatInt(int64(ttl.Seconds()), 10))
	w.Header().Set("X-Origin-TTFB-Ms", strconv.FormatInt(resp.TTFB.Milliseconds(), 10))
	w.Header().Set("X-Transfer-Time-Ms", strconv.FormatInt(transferTime.Milliseconds(), 10))
	w.Header().Set("X-Node-Id", p.config.Server.NodeID)

	if shared {
		w.Header().Set("X-Singleflight", "shared")
	}

	w.WriteHeader(resp.StatusCode)
	w.Write(resp.Body)

	ctx := r.Context()
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	metrics.CacheMisses.Add(ctx, 1)
	metrics.OriginTTFB.Record(ctx, resp.TTFB.Seconds())
	metrics.TransferTime.Record(ctx, transferTime.Seconds(), metric.WithAttributes(metrics.Attr("tier", "origin")))
	metrics.RequestsTotal.Add(ctx, 1, metric.WithAttributes(
		metrics.Attr("status", strconv.Itoa(resp.StatusCode)),
		metrics.Attr("cache_status", "MISS"),
		metrics.Attr("cache_tier", ""),
		metrics.Attr("content_type", resp.ContentType),
	))
}

func cacheKey(r *http.Request) string {
	var buf bytes.Buffer
	buf.WriteString(r.Method)
	buf.WriteByte(':')
	buf.WriteString(r.URL.Path)
	if r.URL.RawQuery != "" {
		buf.WriteByte('?')
		buf.WriteString(r.URL.RawQuery)
	}
	return buf.String()
}

func cloneHeaders(h http.Header) http.Header {
	clone := make(http.Header, len(h))
	for k, v := range h {
		clone[k] = append([]string(nil), v...)
	}
	return clone
}

func canUseSingleFlight(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.Header.Get("Authorization") != "" {
		return false
	}
	return true
}
