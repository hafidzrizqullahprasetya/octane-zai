// Package server — token bucket rate limiter (stdlib only).
// Membatasi laju request ke upstream AutoClaw agar tidak memicu WAF block.
package server

import (
	"context"
	"sync"
	"time"
)

// RateLimiter adalah token bucket sederhana.
// Tokens terisi (refill) secara kontinu pada rate tertentu hingga kapasitas burst.
type RateLimiter struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64 // kapasitas burst
	refillRate float64 // token per detik
	lastRefill time.Time
}

// NewRateLimiter membuat limiter dengan rate token/detik dan burst capacity.
func NewRateLimiter(ratePerSec float64, burst int) *RateLimiter {
	if ratePerSec <= 0 {
		ratePerSec = 1.0 / 1.5 // default 1 req per 1.5 detik
	}
	if burst < 1 {
		burst = 3
	}
	return &RateLimiter{
		tokens:     float64(burst),
		maxTokens:  float64(burst),
		refillRate: ratePerSec,
		lastRefill: time.Now(),
	}
}

// refill menambah token sesuai waktu yang berlalu sejak refill terakhir.
func (l *RateLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(l.lastRefill).Seconds()
	if elapsed > 0 {
		l.tokens += elapsed * l.refillRate
		if l.tokens > l.maxTokens {
			l.tokens = l.maxTokens
		}
		l.lastRefill = now
	}
}

// Wait menunggu hingga ada token tersedia, lalu mengkonsumsinya.
// Menghormati pembatalan ctx.
func (l *RateLimiter) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		l.refill()
		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}
		// Hitung waktu tunggu hingga token berikutnya tersedia
		needed := 1 - l.tokens
		waitDur := time.Duration(needed / l.refillRate * float64(time.Second))
		if waitDur < 10*time.Millisecond {
			waitDur = 10 * time.Millisecond
		}
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitDur):
		}
	}
}
