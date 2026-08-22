package auth

import (
	"sync"
	"time"
)

type attempt struct {
	window       time.Time
	failures     int
	blockedUntil time.Time
}
type Limiter struct {
	mu        sync.Mutex
	entries   map[string]attempt
	lastSweep time.Time
}

func NewLimiter() *Limiter { return &Limiter{entries: map[string]attempt{}} }
func (l *Limiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.sweepLocked(now)
	a := l.entries[ip]
	if now.Sub(a.window) >= time.Minute {
		a = attempt{window: now}
	}
	l.entries[ip] = a
	return now.After(a.blockedUntil) && a.failures < 5
}
func (l *Limiter) Failure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.sweepLocked(now)
	a := l.entries[ip]
	if a.window.IsZero() || now.Sub(a.window) >= time.Minute {
		a = attempt{window: now}
	}
	a.failures++
	if a.failures >= 5 {
		delay := time.Second * time.Duration(1<<min(a.failures-5, 6))
		a.blockedUntil = now.Add(delay)
	}
	l.entries[ip] = a
}
func (l *Limiter) Success(ip string) { l.mu.Lock(); delete(l.entries, ip); l.mu.Unlock() }

func (l *Limiter) sweepLocked(now time.Time) {
	if len(l.entries) < 256 && now.Sub(l.lastSweep) < 5*time.Minute {
		return
	}
	for ip, value := range l.entries {
		if now.Sub(value.window) >= 10*time.Minute && now.After(value.blockedUntil) {
			delete(l.entries, ip)
		}
	}
	l.lastSweep = now
}
