package api

import (
	"net/http"

	embedsvc "github.com/bejix/upstream-ops/backend/embed"
	"github.com/gin-gonic/gin"
)

func registerEmbedSites(g *gin.RouterGroup, d *Deps) {
	if d.Embed == nil {
		return
	}
	eg := g.Group("/embed-sites")

	eg.GET("", func(c *gin.Context) {
		list, err := d.Embed.ListSites()
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": list})
	})

	eg.POST("", func(c *gin.Context) {
		var in embedsvc.SiteInput
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		site, err := d.Embed.CreateSite(in)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"data": site})
	})

	eg.GET("/:id", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		site, err := d.Embed.GetSite(id)
		if err != nil {
			failLookup(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": site})
	})

	eg.PUT("/:id", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		var in embedsvc.SiteInput
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		site, err := d.Embed.UpdateSite(id, in)
		if err != nil {
			failLookupOrBadRequest(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": site})
	})

	eg.DELETE("/:id", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		if err := d.Embed.DeleteSite(id); err != nil {
			failLookup(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
	})
}
