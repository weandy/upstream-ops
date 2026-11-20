package embed

import (
	"bytes"
	"errors"
	"html/template"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// RegisterPublic 注册免管理台鉴权的公开外部嵌入路由与只读状态 JSON API。
func (s *Service) RegisterPublic(r *gin.Engine) {
	if s == nil {
		return
	}
	r.GET("/embed", s.handleOverview)
	r.GET("/embed/candy", s.handleCandy)
	r.GET("/embed/fingerprint", s.handleFingerprint)
	r.GET("/embed/pelican", s.handlePelicanGallery)
	r.GET("/embed/pelican/:run_uuid", s.handlePelicanArtifact)

	r.GET("/api/public/status", s.handlePublicStatus)
	r.OPTIONS("/api/public/status", s.handlePublicStatusOptions)
}

func (s *Service) handleOverview(c *gin.Context) {
	site, err := s.ResolveSite(c.Query("site"))
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	snap, err := s.BuildSiteSnapshot(site)
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	ApplyEmbedHeaders(c, site.AllowedOrigins)
	renderHTMLTemplate(c, ssrTemplates.overview, snap)
}

func (s *Service) handleCandy(c *gin.Context) {
	site, err := s.ResolveSite(c.Query("site"))
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	view, err := s.BuildCandyHistory(site, c.Query("target"), DefaultHistoryLimit)
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	ApplyEmbedHeaders(c, site.AllowedOrigins)
	renderHTMLTemplate(c, ssrTemplates.candy, view)
}

func (s *Service) handleFingerprint(c *gin.Context) {
	site, err := s.ResolveSite(c.Query("site"))
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	view, err := s.BuildFingerprintHistory(site, c.Query("target"), DefaultHistoryLimit)
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	ApplyEmbedHeaders(c, site.AllowedOrigins)
	renderHTMLTemplate(c, ssrTemplates.fingerprint, view)
}

func (s *Service) handlePelicanGallery(c *gin.Context) {
	site, err := s.ResolveSite(c.Query("site"))
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	view, err := s.BuildPelicanGallery(site, c.Query("target"), DefaultPelicanLimit)
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	ApplyEmbedHeaders(c, site.AllowedOrigins)
	renderHTMLTemplate(c, ssrTemplates.pelican, view)
}

func (s *Service) handlePelicanArtifact(c *gin.Context) {
	site, err := s.ResolveSite(c.Query("site"))
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	runUUID := strings.TrimSpace(c.Param("run_uuid"))
	if runUUID == "" {
		c.String(http.StatusBadRequest, "run_uuid is required")
		return
	}
	doc, err := s.ResolvePelicanArtifact(site, runUUID)
	if err != nil {
		s.renderEmbedError(c, err)
		return
	}
	ApplyPelicanSandboxHeaders(c, site.AllowedOrigins)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(doc))
}

func (s *Service) handlePublicStatusOptions(c *gin.Context) {
	site, err := s.ResolveSite(c.Query("site"))
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	ApplyCORSHeaders(c, site.CORSOrigins)
	c.AbortWithStatus(http.StatusNoContent)
}

func (s *Service) handlePublicStatus(c *gin.Context) {
	site, err := s.ResolveSite(c.Query("site"))
	if err != nil {
		if errors.Is(err, ErrSiteNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "embed site not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve embed site"})
		return
	}
	ApplyCORSHeaders(c, site.CORSOrigins)
	snap, err := s.BuildSiteSnapshot(site)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build public status"})
		return
	}
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

func renderHTMLTemplate(c *gin.Context, tpl *template.Template, data any) {
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		c.String(http.StatusInternalServerError, "render failed")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", buf.Bytes())
}
