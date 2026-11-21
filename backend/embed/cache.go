package embed

import (
	"errors"
	"sync"
	"time"
)

const (
	maxSnapshotEntries = 128
	maxSubpageEntries  = 512
	maxArtifactEntries = 64
	maxNegativeEntries = 256
	defaultNegativeTTL = 3 * time.Second
)

type cacheEntry[V any] struct {
	val       V
	expiresAt time.Time
}

// boundedCache 线程安全的有界 TTL 内存缓存，防止公网并发刷接口打穿数据库并严格控制内存上限。
type boundedCache[V any] struct {
	mu      sync.RWMutex
	maxSize int
	items   map[string]cacheEntry[V]
}

func newBoundedCache[V any](maxSize int) *boundedCache[V] {
	if maxSize <= 0 {
		maxSize = 64
	}
	return &boundedCache[V]{
		maxSize: maxSize,
		items:   make(map[string]cacheEntry[V]),
	}
}

func (c *boundedCache[V]) Get(key string, now time.Time) (V, bool) {
	c.mu.RLock()
	entry, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		var zero V
		return zero, false
	}
	if !now.Before(entry.expiresAt) {
		c.mu.Lock()
		if cur, exists := c.items[key]; exists && !now.Before(cur.expiresAt) {
			delete(c.items, key)
		}
		c.mu.Unlock()
		var zero V
		return zero, false
	}
	return entry.val, true
}

func (c *boundedCache[V]) Set(key string, val V, ttl time.Duration, now time.Time) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.maxSize {
		if _, exists := c.items[key]; !exists {
			// 先清理已过期条目。
			for k, e := range c.items {
				if !now.Before(e.expiresAt) {
					delete(c.items, k)
				}
			}
			// 仍满则淘汰最早过期（最旧）的一条，保持 O(N) 简单可控且有界。
			if len(c.items) >= c.maxSize {
				var oldestKey string
				var oldestExp time.Time
				first := true
				for k, e := range c.items {
					if first || e.expiresAt.Before(oldestExp) {
						oldestKey = k
						oldestExp = e.expiresAt
						first = false
					}
				}
				if oldestKey != "" {
					delete(c.items, oldestKey)
				}
			}
		}
	}
	c.items[key] = cacheEntry[V]{
		val:       val,
		expiresAt: now.Add(ttl),
	}
}

func (c *boundedCache[V]) Clear() {
	c.mu.Lock()
	clear(c.items)
	c.mu.Unlock()
}

// singleflightGroup 零依赖泛型并发请求合并，防止缓存过期瞬间多并发同时击穿数据库。
type sfCall[V any] struct {
	wg  sync.WaitGroup
	val V
	err error
}

type singleflightGroup[V any] struct {
	mu sync.Mutex
	m  map[string]*sfCall[V]
}

func (g *singleflightGroup[V]) Do(key string, fn func() (V, error)) (V, bool, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*sfCall[V])
	}
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, true, c.err
	}
	c := &sfCall[V]{}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	return c.val, false, c.err
}

type cachedSiteResult struct {
	site *SiteDTO
}

// embedCache 统一管理公开嵌入页与公开只读 JSON API 的分层进程内微缓存。
type embedCache struct {
	mu         sync.RWMutex
	ttl        time.Duration
	artifactTT time.Duration

	sites        *boundedCache[*SiteDTO]
	snapshots    *boundedCache[*SiteSnapshot]
	overviewHTML *boundedCache[[]byte]
	subpageHTML  *boundedCache[[]byte]
	artifacts    *boundedCache[[]byte]
	negatives    *boundedCache[error]

	sfSite      singleflightGroup[*SiteDTO]
	sfSnapshot  singleflightGroup[*SiteSnapshot]
	sfOverview  singleflightGroup[[]byte]
	sfSubpage   singleflightGroup[[]byte]
	sfArtifact  singleflightGroup[[]byte]
}

func newEmbedCache(ttl time.Duration) *embedCache {
	c := &embedCache{
		sites:        newBoundedCache[*SiteDTO](maxSnapshotEntries),
		snapshots:    newBoundedCache[*SiteSnapshot](maxSnapshotEntries),
		overviewHTML: newBoundedCache[[]byte](maxSnapshotEntries),
		subpageHTML:  newBoundedCache[[]byte](maxSubpageEntries),
		artifacts:    newBoundedCache[[]byte](maxArtifactEntries),
		negatives:    newBoundedCache[error](maxNegativeEntries),
	}
	c.SetTTL(ttl)
	return c
}

func (c *embedCache) SetTTL(ttl time.Duration) {
	if ttl < 0 {
		ttl = 0
	}
	artTTL := ttl * 3
	if ttl > 0 && artTTL < 15*time.Second {
		artTTL = 15 * time.Second
	}
	c.mu.Lock()
	c.ttl = ttl
	c.artifactTT = artTTL
	c.mu.Unlock()
	if ttl == 0 {
		c.InvalidateAll()
	}
}

func (c *embedCache) currentTTLs() (time.Duration, time.Duration) {
	c.mu.RLock()
	ttl, artTTL := c.ttl, c.artifactTT
	c.mu.RUnlock()
	return ttl, artTTL
}

// InvalidateAll 清空所有分区（站点配置变更时调用）。
func (c *embedCache) InvalidateAll() {
	c.sites.Clear()
	c.snapshots.Clear()
	c.overviewHTML.Clear()
	c.subpageHTML.Clear()
	c.artifacts.Clear()
	c.negatives.Clear()
}

// InvalidateSnapshots 清空数据快照与渲染 HTML 分区（目标变更或手动检测后调用）。
func (c *embedCache) InvalidateSnapshots() {
	c.snapshots.Clear()
	c.overviewHTML.Clear()
	c.subpageHTML.Clear()
	c.artifacts.Clear()
	c.negatives.Clear()
}

func isNegativeCacheableErr(err error) bool {
	return errors.Is(err, ErrSiteNotFound) ||
		errors.Is(err, ErrTargetNotVisible) ||
		errors.Is(err, ErrArtifactNotFound)
}
