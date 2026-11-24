package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/bejix/upstream-ops/backend/intelligence"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const pelicanArtifactSandboxCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; base-uri 'none'; form-action 'none'; sandbox"

func registerIntelligence(g *gin.RouterGroup, d *Deps) {
	if d.Intelligence == nil {
		return
	}
	ig := g.Group("/intelligence")

	ig.GET("/detectors", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": d.Intelligence.ListDetectorMetas()})
	})

	ig.GET("/overviews", func(c *gin.Context) {
		list, err := d.Intelligence.ListTargetOverviews()
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": list})
	})

	ig.GET("/compare", func(c *gin.Context) {
		window := strings.TrimSpace(c.Query("window"))
		idsParam := strings.TrimSpace(c.Query("ids"))
		var ids []uint
		if idsParam != "" {
			for _, part := range strings.Split(idsParam, ",") {
				p := strings.TrimSpace(part)
				if p == "" {
					continue
				}
				v, err := strconv.ParseUint(p, 10, 64)
				if err != nil || v == 0 {
					fail(c, http.StatusBadRequest, errors.New("invalid ids parameter"))
					return
				}
				ids = append(ids, uint(v))
			}
		}
		res, err := d.Intelligence.CompareTargets(ids, window)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})

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

	toggleTarget := func(c *gin.Context, enabled bool) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		target, err := d.Intelligence.SetTargetEnabled(id, enabled)
		if err != nil {
			failLookupOrBadRequest(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": target})
	}

	ig.POST("/targets/:id/enable", func(c *gin.Context) {
		toggleTarget(c, true)
	})

	ig.POST("/targets/:id/disable", func(c *gin.Context) {
		toggleTarget(c, false)
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

	ig.GET("/targets/:id/trend", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		series, err := d.Intelligence.GetTargetPerformanceTrend(id, strings.TrimSpace(c.Query("window")))
		if err != nil {
			failLookup(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": series})
	})

	ig.GET("/targets/:id/detectors", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		cfgs, err := d.Intelligence.GetTargetDetectors(id)
		if err != nil {
			failLookup(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": cfgs})
	})

	ig.PUT("/targets/:id/detectors", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		var body struct {
			Items []intelligence.DetectorConfigInput `json:"items"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		cfgs, err := d.Intelligence.SaveTargetDetectors(id, body.Items)
		if err != nil {
			failLookupOrBadRequest(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": cfgs})
	})

	ig.POST("/targets/:id/run", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		var body struct {
			DetectorKey     string `json:"detector_key"`
			ModelName       string `json:"model_name"`
			ReasoningEffort string `json:"reasoning_effort"`
			ParamsJSON      string `json:"params_json"`
		}
		_ = c.ShouldBindJSON(&body)
		key := strings.TrimSpace(body.DetectorKey)
		if key == "" {
			results, err := d.Intelligence.RunEnabledDetectors(c.Request.Context(), id)
			if err != nil {
				failLookupOrBadRequest(c, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"data": results})
			return
		}
		res, err := d.Intelligence.ExecuteDetector(c.Request.Context(), id, key, intelligence.RunOptions{
			Manual:            true,
			ModelOverride:     body.ModelName,
			ReasoningOverride: body.ReasoningEffort,
			ParamsOverride:    body.ParamsJSON,
		})
		if err != nil {
			failLookupOrBadRequest(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})

	ig.GET("/targets/:id/runs", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		detectorKey := strings.TrimSpace(c.Query("detector"))
		limit := queryInt(c, "limit", 48)
		if limit > 200 {
			limit = 200
		}
		runs, err := d.Intelligence.ListTargetRuns(id, detectorKey, limit)
		if err != nil {
			failLookup(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": runs})
	})

	ig.GET("/runs/:run_uuid/artifact", func(c *gin.Context) {
		runUUID := strings.TrimSpace(c.Param("run_uuid"))
		if runUUID == "" {
			fail(c, http.StatusBadRequest, errors.New("run_uuid is required"))
			return
		}
		run, err := d.Intelligence.GetRunArtifact(runUUID)
		if err != nil {
			failLookup(c, err)
			return
		}
		if strings.TrimSpace(run.ArtifactSVG) == "" {
			fail(c, http.StatusNotFound, errors.New("artifact not found"))
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Security-Policy", pelicanArtifactSandboxCSP)
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(run.ArtifactSVG))
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
