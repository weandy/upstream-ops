package embed

import (
	"bytes"
	"errors"
	"html/template"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	cacheStatusHit    = "HIT"
	cacheStatusMiss   = "MISS"
	cacheStatusBypass = "BYPASS"
)

// RegisterPublic 注册免管理台鉴权的公开外部嵌入路由与只读状态 JSON API（统一挂载按 IP 令牌桶限流）。
func (s *Service) RegisterPublic(r *gin.Engine) {
	if s == nil {
		return
	}
	htmlLimit := s.rateLimitMiddleware(false)
	jsonLimit := s.rateLimitMiddleware(true)

	r.GET("/embed", htmlLimit, s.handleOverview)
	r.GET("/embed/candy", htmlLimit, s.handleCandy)
	r.GET("/embed/fingerprint", htmlLimit, s.handleFingerprint)
	r.GET("/embed/pelican", htmlLimit, s.handlePelicanGallery)
	r.GET("/embed/pelican/:run_uuid", htmlLimit, s.handlePelicanArtifact)

	r.GET("/api/public/status", jsonLimit, s.handlePublicStatus)
	r.OPTIONS("/api/public/status", jsonLimit, s.handlePublicStatusOptions)
}

func (s *Service) resolveSiteCached(rawSlug string) (*SiteDTO, error) {
	if s.cache == nil {
		return s.ResolveSite(rawSlug)
	}
	ttl, _ := s.cache.currentTTLs()
	if ttl <= 0 {
		return s.ResolveSite(rawSlug)
	}
	key := "site:" + normalizeSlug(rawSlug)
	now := s.nowUTC()
	if negErr, ok := s.cache.negatives.Get(key, now); ok {
		return nil, negErr
	}
	if site, ok := s.cache.sites.Get(key, now); ok {
		return site, nil
	}
	site, _, err := s.cache.sfSite.Do(key, func() (*SiteDTO, error) {
		nowInner := s.nowUTC()
		if negErr, ok := s.cache.negatives.Get(key, nowInner); ok {
			return nil, negErr
		}
		if cached, ok := s.cache.sites.Get(key, nowInner); ok {
			return cached, nil
		}
		resolved, err := s.ResolveSite(rawSlug)
		if err != nil {
			if isNegativeCacheableErr(err) {
				s.cache.negatives.Set(key, err, defaultNegativeTTL, nowInner)
			}
			return nil, err
		}
		s.cache.sites.Set(key, resolved, ttl, nowInner)
		return resolved, nil
	})
	return site, err
}

func (s *Service) getSnapshotCached(site *SiteDTO) (*SiteSnapshot, string, error) {
	if s.cache == nil {
		s.snapshotBuildCount.Add(1)
		snap, err := s.BuildSiteSnapshot(site)
		return snap, cacheStatusBypass, err
	}
	ttl, _ := s.cache.currentTTLs()
	if ttl <= 0 {
		s.snapshotBuildCount.Add(1)
		snap, err := s.BuildSiteSnapshot(site)
		return snap, cacheStatusBypass, err
	}
	key := "snap:" + site.Slug
	now := s.nowUTC()
	if snap, ok := s.cache.snapshots.Get(key, now); ok {
		return snap, cacheStatusHit, nil
	}
	snap, shared, err := s.cache.sfSnapshot.Do(key, func() (*SiteSnapshot, error) {
		nowInner := s.nowUTC()
		if cached, ok := s.cache.snapshots.Get(key, nowInner); ok {
			return cached, nil
		}
		s.snapshotBuildCount.Add(1)
		built, err := s.BuildSiteSnapshot(site)
		if err != nil {
			return nil, err
		}
		s.cache.snapshots.Set(key, built, ttl, nowInner)
		return built, nil
	})
	if err != nil {
		return nil, cacheStatusMiss, err
	}
	if shared {
		return snap, cacheStatusHit, nil
	}
	return snap, cacheStatusMiss, nil
}

func (s *Service) handleOverview(c *gin.Context) {
	site, err := s.resolveSiteCached(c.Query("site"))
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	ApplyEmbedHeaders(c, site.AllowedOrigins)

	if s.cache == nil {
		s.snapshotBuildCount.Add(1)
		snap, err := s.BuildSiteSnapshot(site)
		if err != nil {
			s.renderEmbedError(c, err)
			return
		}
		c.Header("X-Embed-Cache", cacheStatusBypass)
		renderHTMLTemplate(c, ssrTemplates.overview, snap)
		return
	}

	ttl, _ := s.cache.currentTTLs()
	if ttl <= 0 {
		s.snapshotBuildCount.Add(1)
		snap, err := s.BuildSiteSnapshot(site)
		if err != nil {
			s.renderEmbedError(c, err)
			return
		}
		c.Header("X-Embed-Cache", cacheStatusBypass)
		renderHTMLTemplate(c, ssrTemplates.overview, snap)
		return
	}

	htmlKey := "html:overview:" + site.Slug
	now := s.nowUTC()
	if htmlBytes, ok := s.cache.overviewHTML.Get(htmlKey, now); ok {
		c.Header("X-Embed-Cache", cacheStatusHit)
		c.Data(http.StatusOK, "text/html; charset=utf-8", htmlBytes)
		return
	}

	htmlBytes, shared, err := s.cache.sfOverview.Do(htmlKey, func() ([]byte, error) {
		nowInner := s.nowUTC()
		if cached, ok := s.cache.overviewHTML.Get(htmlKey, nowInner); ok {
			return cached, nil
		}
		snap, _, err := s.getSnapshotCached(site)
		if err != nil {
			return nil, err
		}
		rendered, err := executeHTMLTemplate(ssrTemplates.overview, snap)
		if err != nil {
			return nil, err
		}
		s.cache.overviewHTML.Set(htmlKey, rendered, ttl, nowInner)
		return rendered, nil
	})
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	if shared {
		c.Header("X-Embed-Cache", cacheStatusHit)
	} else {
		c.Header("X-Embed-Cache", cacheStatusMiss)
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", htmlBytes)
}

func (s *Service) handleSubpage(
	c *gin.Context,
	pageKind string,
	tpl *template.Template,
	build func(site *SiteDTO, targetSlug string) (any, error),
) {
	site, err := s.resolveSiteCached(c.Query("site"))
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	targetSlug := c.Query("target")
	ApplyEmbedHeaders(c, site.AllowedOrigins)

	if s.cache == nil {
		data, err := build(site, targetSlug)
		if err != nil {
			s.renderEmbedError(c, err)
			return
		}
		c.Header("X-Embed-Cache", cacheStatusBypass)
		renderHTMLTemplate(c, tpl, data)
		return
	}
	ttl, _ := s.cache.currentTTLs()
	if ttl <= 0 {
		data, err := build(site, targetSlug)
		if err != nil {
			s.renderEmbedError(c, err)
			return
		}
		c.Header("X-Embed-Cache", cacheStatusBypass)
		renderHTMLTemplate(c, tpl, data)
		return
	}

	key := "sub:" + pageKind + ":" + site.Slug + ":" + normalizeSlug(targetSlug)
	now := s.nowUTC()
	if negErr, ok := s.cache.negatives.Get(key, now); ok {
		c.Header("X-Embed-Cache", cacheStatusHit)
		s.renderEmbedError(c, negErr)
		return
	}
	if htmlBytes, ok := s.cache.subpageHTML.Get(key, now); ok {
		c.Header("X-Embed-Cache", cacheStatusHit)
		c.Data(http.StatusOK, "text/html; charset=utf-8", htmlBytes)
		return
	}

	htmlBytes, shared, err := s.cache.sfSubpage.Do(key, func() ([]byte, error) {
		nowInner := s.nowUTC()
		if negErr, ok := s.cache.negatives.Get(key, nowInner); ok {
			return nil, negErr
		}
		if cached, ok := s.cache.subpageHTML.Get(key, nowInner); ok {
			return cached, nil
		}
		data, err := build(site, targetSlug)
		if err != nil {
			if isNegativeCacheableErr(err) {
				s.cache.negatives.Set(key, err, defaultNegativeTTL, nowInner)
			}
			return nil, err
		}
		rendered, err := executeHTMLTemplate(tpl, data)
		if err != nil {
			return nil, err
		}
		s.cache.subpageHTML.Set(key, rendered, ttl, nowInner)
		return rendered, nil
	})
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	if shared {
		c.Header("X-Embed-Cache", cacheStatusHit)
	} else {
		c.Header("X-Embed-Cache", cacheStatusMiss)
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", htmlBytes)
}

func (s *Service) handleCandy(c *gin.Context) {
	s.handleSubpage(c, "candy", ssrTemplates.candy, func(site *SiteDTO, targetSlug string) (any, error) {
		return s.BuildCandyHistory(site, targetSlug, DefaultHistoryLimit)
	})
}

func (s *Service) handleFingerprint(c *gin.Context) {
	s.handleSubpage(c, "fingerprint", ssrTemplates.fingerprint, func(site *SiteDTO, targetSlug string) (any, error) {
		return s.BuildFingerprintHistory(site, targetSlug, DefaultHistoryLimit)
	})
}

func (s *Service) handlePelicanGallery(c *gin.Context) {
	s.handleSubpage(c, "pelican", ssrTemplates.pelican, func(site *SiteDTO, targetSlug string) (any, error) {
		return s.BuildPelicanGallery(site, targetSlug, DefaultPelicanLimit)
	})
}

func (s *Service) handlePelicanArtifact(c *gin.Context) {
	site, err := s.resolveSiteCached(c.Query("site"))
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	runUUID := strings.TrimSpace(c.Param("run_uuid"))
	if runUUID == "" {
		c.String(http.StatusBadRequest, "run_uuid is required")
		return
	}

	if s.cache == nil {
		doc, err := s.ResolvePelicanArtifact(site, runUUID)
		if err != nil {
			s.renderEmbedError(c, err)
			return
		}
		ApplyPelicanSandboxHeaders(c, site.AllowedOrigins)
		c.Header("X-Embed-Cache", cacheStatusBypass)
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(doc))
		return
	}
	ttl, artTTL := s.cache.currentTTLs()
	if ttl <= 0 {
		doc, err := s.ResolvePelicanArtifact(site, runUUID)
		if err != nil {
			s.renderEmbedError(c, err)
			return
		}
		ApplyPelicanSandboxHeaders(c, site.AllowedOrigins)
		c.Header("X-Embed-Cache", cacheStatusBypass)
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(doc))
		return
	}

	key := "artifact:" + site.Slug + ":" + runUUID
	now := s.nowUTC()
	if negErr, ok := s.cache.negatives.Get(key, now); ok {
		c.Header("X-Embed-Cache", cacheStatusHit)
		s.renderEmbedError(c, negErr)
		return
	}
	if docBytes, ok := s.cache.artifacts.Get(key, now); ok {
		ApplyPelicanSandboxHeaders(c, site.AllowedOrigins)
		c.Header("X-Embed-Cache", cacheStatusHit)
		c.Data(http.StatusOK, "text/html; charset=utf-8", docBytes)
		return
	}

	docBytes, shared, err := s.cache.sfArtifact.Do(key, func() ([]byte, error) {
		nowInner := s.nowUTC()
		if negErr, ok := s.cache.negatives.Get(key, nowInner); ok {
			return nil, negErr
		}
		if cached, ok := s.cache.artifacts.Get(key, nowInner); ok {
			return cached, nil
		}
		doc, err := s.ResolvePelicanArtifact(site, runUUID)
		if err != nil {
			if isNegativeCacheableErr(err) {
				s.cache.negatives.Set(key, err, defaultNegativeTTL, nowInner)
			}
			return nil, err
		}
		raw := []byte(doc)
		s.cache.artifacts.Set(key, raw, artTTL, nowInner)
		return raw, nil
	})
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	ApplyPelicanSandboxHeaders(c, site.AllowedOrigins)
	if shared {
		c.Header("X-Embed-Cache", cacheStatusHit)
	} else {
		c.Header("X-Embed-Cache", cacheStatusMiss)
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", docBytes)
}

func (s *Service) handlePublicStatusOptions(c *gin.Context) {
	site, err := s.resolveSiteCached(c.Query("site"))
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	ApplyCORSHeaders(c, site.CORSOrigins)
	c.AbortWithStatus(http.StatusNoContent)
}

func (s *Service) handlePublicStatus(c *gin.Context) {
	site, err := s.resolveSiteCached(c.Query("site"))
	if err != nil {
		if errors.Is(err, ErrSiteNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "embed site not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve embed site"})
		return
	}
	// CORS 响应头始终基于当前请求 Origin 动态计算，绝不与快照缓存共享。
	ApplyCORSHeaders(c, site.CORSOrigins)
	snap, cacheStatus, err := s.getSnapshotCached(site)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build public status"})
		return
	}
	c.Header("X-Embed-Cache", cacheStatus)
	c.JSON(http.StatusOK, gin.H{"data": snap})
}

func (s *Service) renderEmbedError(c *gin.Context, err error) {
	ApplyEmbedHeaders(c, nil)
	switch {
	case errors.Is(err, ErrSiteNotFound):
		c.String(http.StatusNotFound, "embed site not found")
	case errors.Is(err, ErrTargetNotVisible):
		c.String(http.StatusNotFound, "target not available")
	case errors.Is(err, ErrArtifactNotFound):
		c.String(http.StatusNotFound, "artifact not available")
	default:
		c.String(http.StatusInternalServerError, "internal error")
	}
}

func executeHTMLTemplate(tpl *template.Template, data any) ([]byte, error) {
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func renderHTMLTemplate(c *gin.Context, tpl *template.Template, data any) {
	raw, err := executeHTMLTemplate(tpl, data)
	if err != nil {
		c.String(http.StatusInternalServerError, "render failed")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", raw)
}
