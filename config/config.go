package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
	Origin OriginConfig `yaml:"origin"`
	Cache  CacheConfig  `yaml:"cache"`
	TTL    []TTLRule    `yaml:"ttl_rules"`
}

type ServerConfig struct {
	Port   int    `yaml:"port"`
	NodeID string `yaml:"node_id"`
}

type OriginConfig struct {
	URL        string           `yaml:"url"`
	Timeout    time.Duration    `yaml:"timeout"`
	Protection ProtectionConfig `yaml:"protection"`
}

type ProtectionConfig struct {
	SingleFlight SingleFlightConfig `yaml:"single_flight"`
	RateLimit    RateLimitConfig    `yaml:"rate_limit"`
}

type SingleFlightConfig struct {
	Enabled bool `yaml:"enabled"`
}

type RateLimitConfig struct {
	Enabled bool          `yaml:"enabled"`
	RPS     float64       `yaml:"rps"`
	Burst   int           `yaml:"burst"`
	Timeout time.Duration `yaml:"timeout"`
}

type CacheConfig struct {
	Memory     MemoryCacheConfig `yaml:"memory"`
	Disk       DiskCacheConfig   `yaml:"disk"`
	DefaultTTL time.Duration     `yaml:"default_ttl"`
}

type MemoryCacheConfig struct {
	MaxSizeMB int `yaml:"max_size_mb"`
}

type DiskCacheConfig struct {
	Path      string `yaml:"path"`
	MaxSizeMB int    `yaml:"max_size_mb"`
}

type TTLRule struct {
	Pattern string        `yaml:"pattern"`
	TTL     time.Duration `yaml:"ttl"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Server: ServerConfig{Port: 8080, NodeID: "node-1"},
		Origin: OriginConfig{
			Timeout: 30 * time.Second,
			Protection: ProtectionConfig{
				SingleFlight: SingleFlightConfig{Enabled: true},
				RateLimit: RateLimitConfig{
					Enabled: false,
					RPS:     100,
					Burst:   50,
					Timeout: 5 * time.Second,
				},
			},
		},
		Cache: CacheConfig{
			Memory:     MemoryCacheConfig{MaxSizeMB: 256},
			Disk:       DiskCacheConfig{Path: "./cache_data", MaxSizeMB: 2048},
			DefaultTTL: time.Hour,
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) GetTTL(contentType string) time.Duration {
	for _, rule := range c.TTL {
		if matchPattern(rule.Pattern, contentType) {
			return rule.TTL
		}
	}
	return c.Cache.DefaultTTL
}

func matchPattern(pattern, contentType string) bool {
	if pattern == contentType {
		return true
	}
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		return strings.HasPrefix(contentType, prefix+"/")
	}
	matched, _ := filepath.Match(pattern, contentType)
	return matched
}
