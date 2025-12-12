package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewDefault(t *testing.T) {
	limiter := NewDefault()
	if limiter == nil {
		t.Fatal("expected non-nil limiter")
	}
	if limiter.core == nil {
		t.Error("expected non-nil core limiter")
	}
	if limiter.search == nil {
		t.Error("expected non-nil search limiter")
	}
	if limiter.graphql == nil {
		t.Error("expected non-nil graphql limiter")
	}
}

func TestRateLimiter_AllowCore(t *testing.T) {
	limiter := NewDefault()

	// First few requests should be allowed due to burst
	for i := 0; i < 5; i++ {
		if !limiter.AllowCore() {
			t.Errorf("expected request %d to be allowed", i)
		}
	}
}

func TestRateLimiter_WaitCore(t *testing.T) {
	limiter := NewDefault()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := limiter.WaitCore(ctx)
	if err != nil {
		t.Errorf("expected first wait to succeed, got: %v", err)
	}

	stats := limiter.GetStats()
	if stats.CoreWaits != 1 {
		t.Errorf("expected 1 core wait, got %d", stats.CoreWaits)
	}
}

func TestRateLimiter_WaitSearch(t *testing.T) {
	limiter := NewDefault()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := limiter.WaitSearch(ctx)
	if err != nil {
		t.Errorf("expected first wait to succeed, got: %v", err)
	}

	stats := limiter.GetStats()
	if stats.SearchWaits != 1 {
		t.Errorf("expected 1 search wait, got %d", stats.SearchWaits)
	}
}

func TestRateLimiter_WaitGraphQL(t *testing.T) {
	limiter := NewDefault()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := limiter.WaitGraphQL(ctx)
	if err != nil {
		t.Errorf("expected first wait to succeed, got: %v", err)
	}

	stats := limiter.GetStats()
	if stats.GraphQLWaits != 1 {
		t.Errorf("expected 1 graphql wait, got %d", stats.GraphQLWaits)
	}
}

func TestRateLimiter_ResetStats(t *testing.T) {
	limiter := NewDefault()
	ctx := context.Background()

	_ = limiter.WaitCore(ctx)
	_ = limiter.WaitSearch(ctx)

	limiter.ResetStats()
	stats := limiter.GetStats()

	if stats.CoreWaits != 0 || stats.SearchWaits != 0 {
		t.Error("expected stats to be reset to zero")
	}
}

func TestRetryWithBackoff_Success(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		BackoffFactor:  2.0,
	}

	attempts := 0
	err := RetryWithBackoff(context.Background(), cfg, func() error {
		attempts++
		return nil
	})

	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", attempts)
	}
}

func TestRetryWithBackoff_EventualSuccess(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		BackoffFactor:  2.0,
	}

	attempts := 0
	err := RetryWithBackoff(context.Background(), cfg, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary error")
		}
		return nil
	})

	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryWithBackoff_AllFail(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		BackoffFactor:  2.0,
	}

	testErr := errors.New("persistent error")
	attempts := 0
	err := RetryWithBackoff(context.Background(), cfg, func() error {
		attempts++
		return testErr
	})

	if err != testErr {
		t.Errorf("expected test error, got: %v", err)
	}
	if attempts != 3 { // initial + 2 retries
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryWithBackoff_ContextCancelled(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:     5,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
		BackoffFactor:  2.0,
	}

	ctx, cancel := context.WithCancel(context.Background())

	attempts := 0
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := RetryWithBackoff(ctx, cfg, func() error {
		attempts++
		return errors.New("error")
	})

	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()

	if cfg.MaxRetries != 3 {
		t.Errorf("expected MaxRetries 3, got %d", cfg.MaxRetries)
	}
	if cfg.InitialBackoff != 1*time.Second {
		t.Errorf("expected InitialBackoff 1s, got %v", cfg.InitialBackoff)
	}
	if cfg.MaxBackoff != 30*time.Second {
		t.Errorf("expected MaxBackoff 30s, got %v", cfg.MaxBackoff)
	}
	if cfg.BackoffFactor != 2.0 {
		t.Errorf("expected BackoffFactor 2.0, got %f", cfg.BackoffFactor)
	}
}

func TestDefaultLimits(t *testing.T) {
	limits := DefaultLimits()

	if limits.CoreRequestsPerHour != 5000 {
		t.Errorf("expected CoreRequestsPerHour 5000, got %d", limits.CoreRequestsPerHour)
	}
	if limits.SearchRequestsPerMinute != 30 {
		t.Errorf("expected SearchRequestsPerMinute 30, got %d", limits.SearchRequestsPerMinute)
	}
	if limits.GraphQLPointsPerHour != 5000 {
		t.Errorf("expected GraphQLPointsPerHour 5000, got %d", limits.GraphQLPointsPerHour)
	}
}
