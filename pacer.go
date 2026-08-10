package main

import (
	"context"
	"sync"
	"time"
)

type pacer struct {
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
	mu     sync.Mutex
}

func newPacer(rate, burst int) *pacer {
	if rate <= 0 || burst <= 0 {
		return nil
	}
	return &pacer{
		rate:   float64(rate),
		burst:  float64(burst),
		tokens: float64(burst),
		last:   time.Now(),
	}
}

func (p *pacer) await(ctx context.Context, size float64) error {
	if p == nil || size <= 0 {
		return nil
	}

	for {
		now := time.Now()
		p.mu.Lock()
		elapsed := now.Sub(p.last).Seconds()
		if elapsed > 0 {
			p.tokens = min(p.burst, p.tokens+elapsed*p.rate)
			p.last = now
		}
		if p.tokens >= size {
			p.tokens -= size
			p.mu.Unlock()
			return nil
		}
		wait := time.Duration((size - p.tokens) / p.rate * float64(time.Second))
		p.mu.Unlock()

		if wait <= 0 {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
