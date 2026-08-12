package main

import (
	"context"
	"sync"
	"time"
)

type pacer struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	tokens  float64
	updated time.Time
}

func newPacer(rate, burst float64) *pacer {
	return &pacer{rate: rate, burst: burst, tokens: burst, updated: time.Now()}
}

func (p *pacer) await(ctx context.Context, amount float64) error {
	for {
		p.mu.Lock()
		now := time.Now()
		p.tokens = min(p.burst, p.tokens+now.Sub(p.updated).Seconds()*p.rate)
		p.updated = now
		if p.tokens >= amount {
			p.tokens -= amount
			p.mu.Unlock()
			return nil
		}
		wait := time.Duration((amount - p.tokens) / p.rate * float64(time.Second))
		p.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
