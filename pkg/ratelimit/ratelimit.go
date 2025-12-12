// Package ratelimit provides rate limiting functionality for GitHub API calls.
// It implements client-side rate limiting to prevent hitting GitHub's API limits
// and provides graceful degradation instead of hard failures.
package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// GitHubLimits defines the rate limits for different GitHub API endpoints
type GitHubLimits struct {
	// CoreRequestsPerHour is the limit for authenticated core API requests (default: 5000/hour)
	CoreRequestsPerHour int
	// SearchRequestsPerMinute is the limit for search API requests (default: 30/min)
	SearchRequestsPerMinute int
	// GraphQLPointsPerHour is the limit for GraphQL API points (default: 5000/hour)
	GraphQLPointsPerHour int
}

// DefaultLimits returns the default GitHub API rate limits for authenticated users
func DefaultLimits() GitHubLimits {
	return GitHubLimits{
		CoreRequestsPerHour:     5000,
		SearchRequestsPerMinute: 30,
		GraphQLPointsPerHour:    5000,
	}
}

// RateLimiter provides rate limiting for GitHub API calls
type RateLimiter struct {
	core    *rate.Limiter
	search  *rate.Limiter
	graphql *rate.Limiter
	mu      sync.RWMutex

	// Stats for monitoring
	stats Stats
}

// Stats tracks rate limiter statistics
type Stats struct {
	CoreWaits    int64
	SearchWaits  int64
	GraphQLWaits int64
	TotalWaitMs  int64
}

// New creates a new RateLimiter with the specified limits
func New(limits GitHubLimits) *RateLimiter {
	// Convert hourly/minute limits to per-second rates
	// Use 90% of the limit to provide safety margin
	coreRate := rate.Limit(float64(limits.CoreRequestsPerHour) * 0.9 / 3600)
	searchRate := rate.Limit(float64(limits.SearchRequestsPerMinute) * 0.9 / 60)
	graphqlRate := rate.Limit(float64(limits.GraphQLPointsPerHour) * 0.9 / 3600)

	return &RateLimiter{
		// Burst allows some requests to go through immediately
		core:    rate.NewLimiter(coreRate, 10),
		search:  rate.NewLimiter(searchRate, 5),
		graphql: rate.NewLimiter(graphqlRate, 10),
	}
}

// NewDefault creates a RateLimiter with default GitHub limits
func NewDefault() *RateLimiter {
	return New(DefaultLimits())
}

// WaitCore waits for permission to make a core API request
func (r *RateLimiter) WaitCore(ctx context.Context) error {
	start := time.Now()
	err := r.core.Wait(ctx)
	if err == nil {
		r.mu.Lock()
		r.stats.CoreWaits++
		r.stats.TotalWaitMs += time.Since(start).Milliseconds()
		r.mu.Unlock()
	}
	return err
}

// WaitSearch waits for permission to make a search API request
func (r *RateLimiter) WaitSearch(ctx context.Context) error {
	start := time.Now()
	err := r.search.Wait(ctx)
	if err == nil {
		r.mu.Lock()
		r.stats.SearchWaits++
		r.stats.TotalWaitMs += time.Since(start).Milliseconds()
		r.mu.Unlock()
	}
	return err
}

// WaitGraphQL waits for permission to make a GraphQL API request
func (r *RateLimiter) WaitGraphQL(ctx context.Context) error {
	start := time.Now()
	err := r.graphql.Wait(ctx)
	if err == nil {
		r.mu.Lock()
		r.stats.GraphQLWaits++
		r.stats.TotalWaitMs += time.Since(start).Milliseconds()
		r.mu.Unlock()
	}
	return err
}

// AllowCore checks if a core API request can proceed without waiting
func (r *RateLimiter) AllowCore() bool {
	return r.core.Allow()
}

// AllowSearch checks if a search API request can proceed without waiting
func (r *RateLimiter) AllowSearch() bool {
	return r.search.Allow()
}

// AllowGraphQL checks if a GraphQL API request can proceed without waiting
func (r *RateLimiter) AllowGraphQL() bool {
	return r.graphql.Allow()
}

// GetStats returns the current rate limiter statistics
func (r *RateLimiter) GetStats() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.stats
}

// ResetStats resets the rate limiter statistics
func (r *RateLimiter) ResetStats() {
	r.mu.Lock()
	r.stats = Stats{}
	r.mu.Unlock()
}

// ReserveN reserves n tokens from the core limiter and returns a Reservation
func (r *RateLimiter) ReserveN(n int) *rate.Reservation {
	return r.core.ReserveN(time.Now(), n)
}

// SetBurst sets the burst size for all limiters
func (r *RateLimiter) SetBurst(core, search, graphql int) {
	r.core.SetBurst(core)
	r.search.SetBurst(search)
	r.graphql.SetBurst(graphql)
}

// RetryConfig defines retry behavior for rate-limited requests
type RetryConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64
}

// DefaultRetryConfig returns a sensible default retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
	}
}

// RetryWithBackoff executes a function with exponential backoff on rate limit errors
func RetryWithBackoff(ctx context.Context, cfg RetryConfig, fn func() error) error {
	backoff := cfg.InitialBackoff

	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Don't wait after the last attempt
		if attempt == cfg.MaxRetries {
			break
		}

		// Wait with exponential backoff
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		// Increase backoff for next iteration
		backoff = time.Duration(float64(backoff) * cfg.BackoffFactor)
		if backoff > cfg.MaxBackoff {
			backoff = cfg.MaxBackoff
		}
	}

	return lastErr
}
