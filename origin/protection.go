package origin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
)

var ErrRateLimited = errors.New("origin rate limit exceeded")

type Response struct {
	Body        []byte
	Headers     http.Header
	StatusCode  int
	ContentType string
	TTFB        time.Duration
}

type ProtectionConfig struct {
	SingleFlightEnabled bool
	RateLimitEnabled    bool
	RateLimitRPS        float64
	RateLimitBurst      int
	RateLimitTimeout    time.Duration
}

type Protection struct {
	group   singleflight.Group
	limiter *rate.Limiter
	config  ProtectionConfig
}

func NewProtection(cfg ProtectionConfig) *Protection {
	var limiter *rate.Limiter
	if cfg.RateLimitEnabled {
		limiter = rate.NewLimiter(rate.Limit(cfg.RateLimitRPS), cfg.RateLimitBurst)
	}

	return &Protection{
		limiter: limiter,
		config:  cfg,
	}
}

func (p *Protection) Do(ctx context.Context, key string, useSingleFlight bool, fn func() (*Response, error)) (*Response, bool, error) {
	if p.config.RateLimitEnabled {
		waitCtx, cancel := context.WithTimeout(ctx, p.config.RateLimitTimeout)
		defer cancel()

		if err := p.limiter.Wait(waitCtx); err != nil {
			return nil, false, ErrRateLimited
		}
	}

	if !p.config.SingleFlightEnabled || !useSingleFlight {
		resp, err := fn()
		return resp, false, err
	}

	result, err, shared := p.group.Do(key, func() (interface{}, error) {
		return fn()
	})

	if err != nil {
		return nil, shared, err
	}

	return result.(*Response), shared, nil
}
