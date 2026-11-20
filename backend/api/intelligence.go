package api

import (
	"errors"
	"net/http"

	"github.com/bejix/upstream-ops/backend/intelligence"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func registerIntelligence(g *gin.RouterGroup, d *Deps) {
	if d.Intelligence == nil {
		return
	}
	ig := g.Group("/intelligence")

	ig.GET("/targets", func(c *gin.Context) {
		list, err := d.Intelligence.ListTargets()
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": list})
	})

	ig.POST("/targets", func(c *gin.Context) {
		var in intelligence.TargetInput
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		target, err := d.Intelligence.CreateTarget(in)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"data": target})
	})

	ig.GET("/targets/:id", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		target, err := d.Intelligence.GetTarget(id)
		if err != nil {
			failLookup(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": target})
	})

	ig.PUT("/targets/:id", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		var in intelligence.TargetInput
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		target, err := d.Intelligence.UpdateTarget(id, in)
		if err != nil {
			failLookupOrBadRequest(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": target})
	})

	ig.DELETE("/targets/:id", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		if err := d.Intelligence.DeleteTarget(id); err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
	})

	ig.POST("/targets/:id/ensure-key", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		target, err := d.Intelligence.EnsureChannelKey(c.Request.Context(), id)
		if err != nil {
			failLookupOrBadRequest(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": target})
	})

	ig.GET("/targets/:id/models", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		models, err := d.Intelligence.FetchTargetModels(c.Request.Context(), id)
		if err != nil {
			failLookupOrBadRequest(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": models})
	})

	ig.POST("/targets/:id/liveness", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		var body struct {
			ModelName string `json:"model_name"`
		}
		_ = c.ShouldBindJSON(&body)
		sample, err := d.Intelligence.RunLivenessNow(c.Request.Context(), id, body.ModelName)
		if err != nil {
			failLookupOrBadRequest(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": sample})
	})

	ig.GET("/targets/:id/overview", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		ov, err := d.Intelligence.GetTargetOverview(id)
		if err != nil {
			failLookup(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": ov})
	})
}

func parseRequiredUintParam(c *gin.Context, name string) (uint, bool) {
	v, err := parseUintParam(c, name)
	if err != nil || v == 0 {
		fail(c, http.StatusBadRequest, errors.New("invalid id"))
		return 0, false
	}
	return v, true
}

func failLookup(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, http.StatusNotFound, err)
		return
	}
	fail(c, http.StatusInternalServerError, err)
}

func failLookupOrBadRequest(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, http.StatusNotFound, err)
		return
	}
	fail(c, http.StatusBadRequest, err)
}
