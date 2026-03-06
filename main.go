package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"

	"small-cdn/cache"
	"small-cdn/config"
	"small-cdn/metrics"
	"small-cdn/proxy"
)

func main() {
	godotenv.Load()

	log.Printf("env: OTEL_EXPORTER_OTLP_ENDPOINT=%s OTEL_DEBUG=%s CDN_NODE_ID=%s",
		os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		os.Getenv("OTEL_DEBUG"),
		os.Getenv("CDN_NODE_ID"))

	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	memCache, err := cache.NewMemoryCache(cfg.Cache.Memory.MaxSizeMB)
	if err != nil {
		log.Fatalf("failed to create memory cache: %v", err)
	}

	diskCache, err := cache.NewDiskCache(cfg.Cache.Disk.Path, cfg.Cache.Disk.MaxSizeMB)
	if err != nil {
		log.Fatalf("failed to create disk cache: %v", err)
	}
	defer diskCache.Close()

	tieredCache := cache.NewTieredCache(memCache, diskCache)

	ctx := context.Background()
	shutdownMetrics, err := metrics.Init(ctx, cfg.Server.NodeID, &cacheStatsAdapter{cache: tieredCache})
	if err != nil {
		log.Fatalf("failed to init metrics: %v", err)
	}
	defer shutdownMetrics(ctx)

	p, err := proxy.New(cfg, tieredCache)
	if err != nil {
		log.Fatalf("failed to create proxy: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/cache", cacheHandler(tieredCache))
	mux.Handle("/", p)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("version: 1.0.0")
	log.Printf("starting CDN node %s on %s, origin: %s", cfg.Server.NodeID, addr, cfg.Origin.URL)
	log.Printf("memory cache: %d MB, disk cache: %d MB at %s",
		cfg.Cache.Memory.MaxSizeMB, cfg.Cache.Disk.MaxSizeMB, cfg.Cache.Disk.Path)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

type cacheStatsAdapter struct {
	cache *cache.TieredCache
}

func (a *cacheStatsAdapter) MemoryStats() metrics.CacheStats {
	s := a.cache.MemoryStats()
	return metrics.CacheStats{Entries: s.Entries, SizeBytes: s.SizeBytes}
}

func (a *cacheStatsAdapter) DiskStats() metrics.CacheStats {
	s := a.cache.DiskStats()
	return metrics.CacheStats{Entries: s.Entries, SizeBytes: s.SizeBytes}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func cacheHandler(c *cache.TieredCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			c.Clear()
			w.Write([]byte(`{"cleared":true}`))
			return
		}

		memStats := c.MemoryStats()
		diskStats := c.DiskStats()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"memory":{"entries":%d,"size_bytes":%d,"hits":%d,"misses":%d},"disk":{"entries":%d,"size_bytes":%d,"hits":%d,"misses":%d}}`,
			memStats.Entries, memStats.SizeBytes, memStats.Hits, memStats.Misses,
			diskStats.Entries, diskStats.SizeBytes, diskStats.Hits, diskStats.Misses)
	}
}
