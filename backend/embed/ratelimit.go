package embed

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	defaultMaxTrackedIPs = 10000
	ipBucketIdleExpiry   = 5 * time.Minute
	ipSweepInterval      = time.Minute
)

type ipBucket struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
}

// ipRateLimiter 轻量按 IP 令牌桶限流器，支持热更新、硬上限防 OOM 与惰性回收。
type ipRateLimiter struct {
	mu          sync.Mutex
	enabled     bool
	rps         float64
	burst       float64
	maxTracked  int
	buckets     map[string]*ipBucket
	lastSweepAt time.Time
	nowFunc     func() time.Time
}

func newIPRateLimiter(enabled bool, rps float64, burst int, nowFunc func() time.Time) *ipRateLimiter {
	if nowFunc == nil {
		nowFunc = func() time.Time { return time.Now().UTC() }
	}
	l := &ipRateLimiter{
		maxTracked: defaultMaxTrackedIPs,
		buckets:    make(map[string]*ipBucket),
		nowFunc:    nowFunc,
	}
	l.Configure(enabled, rps, burst)
	return l
}

func (l *ipRateLimiter) Configure(enabled bool, rps float64, burst int) {
	if rps <= 0 {
		rps = 5.0
	}
	if burst <= 0 {
		burst = 30
	}
	l.mu.Lock()
	l.enabled = enabled
	l.rps = rps
	l.burst = float64(burst)
	if !enabled {
		clear(l.buckets)
	}
	l.mu.Unlock()
}

// Allow 判断给定 IP 本次请求是否放行；被限流时返回建议等待秒数（>= 1）。
func (l *ipRateLimiter) Allow(ip string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.enabled {
		return true, 0
	}
	now := l.nowFunc()
	if l.lastSweepAt.IsZero() {
		l.lastSweepAt = now
	} else if now.Sub(l.lastSweepAt) >= ipSweepInterval {
		l.sweepExpiredLocked(now)
		l.lastSweepAt = now
	}

	key := strings.TrimSpace(ip)
	if key == "" {
		key = "unknown"
	}

	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxTracked {
			l.sweepExpiredLocked(now)
			if len(l.buckets) >= l.maxTracked {
				l.evictOldestLocked()
			}
		}
		b = &ipBucket{
			tokens:     l.burst,
			lastRefill: now,
			lastSeen:   now,
		}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.lastRefill).Seconds()
		if elapsed > 0 {
			b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rps)
			b.lastRefill = now
		}
		b.lastSeen = now
	}

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true, 0
	}

	needed := 1.0 - b.tokens
	waitSec := int(math.Ceil(needed / l.rps))
	if waitSec < 1 {
		waitSec = 1
	}
	return false, waitSec
}

func (l *ipRateLimiter) TrackedCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

func (l *ipRateLimiter) sweepExpiredLocked(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.lastSeen) >= ipBucketIdleExpiry {
			delete(l.buckets, k)
		}
	}
}

func (l *ipRateLimiter) evictOldestLocked() {
	var oldestKey string
	var oldestSeen time.Time
	first := true
	for k, b := range l.buckets {
		if first || b.lastSeen.Before(oldestSeen) {
			oldestKey = k
			oldestSeen = b.lastSeen
			first = false
		}
	}
	if oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}

const rateLimitHTMLBody = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>请求过于频繁</title>
<style>
body{margin:0;padding:24px;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#f6f2ea;color:#1e2420;display:grid;place-items:center;min-height:180px}
.box{max-width:420px;background:#fffdf9;border:1px solid #e4dccd;border-radius:10px;padding:18px 20px;text-align:center}
h1{margin:0 0 6px;font-size:16px}
p{margin:0;font-size:13px;color:#616a63}
</style>
</head>
<body>
<div class="box">
  <h1>请求过于频繁（429）</h1>
  <p>请等待 %d 秒后再刷新查看。</p>
</div>
</body>
</html>`

func (s *Service) rateLimitMiddleware(isJSON bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if s == nil || s.limiter == nil {
			c.Next()
			return
		}
		allowed, retryAfter := s.limiter.Allow(c.ClientIP())
		if allowed {
			c.Next()
			return
		}
		c.Header("Retry-After", strconv.Itoa(retryAfter))
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		if isJSON {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":               "rate limit exceeded",
				"retry_after_seconds": retryAfter,
			})
			return
		}
		// HTML 429 页刻意不含 <meta http-equiv="refresh">，防止客户端继续死循环撞 429。
		ApplyEmbedHeaders(c, nil)
		c.Data(http.StatusTooManyRequests, "text/html; charset=utf-8", []byte(fmt.Sprintf(rateLimitHTMLBody, retryAfter)))
		c.Abort()
	}
}
