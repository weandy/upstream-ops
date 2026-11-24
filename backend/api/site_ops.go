package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/bejix/upstream-ops/backend/siteops"
	"github.com/gin-gonic/gin"
)

func registerSiteOps(g *gin.RouterGroup, d *Deps) {
	if d.SiteOps == nil {
		return
	}

	// P0–P2 鉴权统一跟随全局 AuthMiddleware；P3 高危写操作端点将单独挂载强制鉴权守卫。
	sg := g.Group("/site-ops")

	sg.GET("/summary", func(c *gin.Context) {
		res, err := d.SiteOps.GetSummary(c.Request.Context())
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})

	sg.GET("/instances", func(c *gin.Context) {
		list, err := d.SiteOps.ListInstances(c.Request.Context())
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": list})
	})

	sg.POST("/instances", func(c *gin.Context) {
		var in siteops.CreateInstanceInput
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		created, err := d.SiteOps.CreateInstance(c.Request.Context(), in)
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"data": created})
	})

	sg.PUT("/instances/:id", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		var in siteops.UpdateInstanceInput
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		updated, err := d.SiteOps.UpdateInstance(c.Request.Context(), id, in)
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": updated})
	})

	sg.DELETE("/instances/:id", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		if err := d.SiteOps.DeleteInstance(c.Request.Context(), id); err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
	})

	sg.POST("/instances/:id/probe", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		report, err := d.SiteOps.ProbeInstance(c.Request.Context(), id)
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": report})
	})

	sg.POST("/instances/:id/refresh", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		res, err := d.SiteOps.RefreshInstance(c.Request.Context(), id)
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})

	sg.GET("/instances/:id/overview", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		res, err := d.SiteOps.GetOverview(c.Request.Context(), id)
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})

	sg.GET("/instances/:id/trends", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		res, err := d.SiteOps.GetTrends(c.Request.Context(), id, strings.TrimSpace(c.Query("range")))
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})

	sg.GET("/instances/:id/groups", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		res, err := d.SiteOps.GetGroups(c.Request.Context(), id)
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})

	sg.GET("/instances/:id/events", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		limit := 100
		if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil || parsed <= 0 {
				fail(c, http.StatusBadRequest, errors.New("invalid limit"))
				return
			}
			limit = parsed
		}
		res, err := d.SiteOps.ListEvents(
			c.Request.Context(),
			id,
			strings.TrimSpace(c.Query("state")),
			strings.TrimSpace(c.Query("severity")),
			limit,
		)
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})

	sg.GET("/instances/:id/accounts", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		view := strings.TrimSpace(c.Query("view"))
		switch view {
		case "", "status":
			res, err := d.SiteOps.GetAccountsStatus(
				c.Request.Context(),
				id,
				strings.TrimSpace(c.Query("status")),
				strings.TrimSpace(c.Query("platform")),
			)
			if err != nil {
				failSiteOps(c, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"data": res})
		case "efficiency":
			res, err := d.SiteOps.GetAccountsEfficiency(c.Request.Context(), id)
			if err != nil {
				failSiteOps(c, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"data": res})
		default:
			fail(c, http.StatusBadRequest, errors.New("unsupported accounts view"))
		}
	})

	sg.GET("/instances/:id/pool-forecast", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		res, err := d.SiteOps.GetPoolForecast(c.Request.Context(), id)
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})

	sg.PUT("/instances/:id/accounts/:account_id/cost", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		accID, err := strconv.ParseInt(strings.TrimSpace(c.Param("account_id")), 10, 64)
		if err != nil || accID <= 0 {
			fail(c, http.StatusBadRequest, errors.New("invalid account_id"))
			return
		}
		var in siteops.UpdateAccountCostInput
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		res, err := d.SiteOps.UpdateAccountCost(c.Request.Context(), id, accID, in)
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})

	sg.POST("/instances/:id/report/preview", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		res, err := d.SiteOps.PreviewDailyReport(c.Request.Context(), id)
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})

	sg.POST("/instances/:id/report/send", func(c *gin.Context) {
		id, ok := parseRequiredUintParam(c, "id")
		if !ok {
			return
		}
		res, err := d.SiteOps.SendDailyReport(c.Request.Context(), id)
		if err != nil {
			failSiteOps(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})
}

func failSiteOps(c *gin.Context, err error) {
	switch {
	case errors.Is(err, siteops.ErrAuthRequired):
		c.JSON(http.StatusForbidden, gin.H{"code": siteops.ErrCodeAuthRequired, "error": siteops.ErrAuthRequired.Error()})
	case errors.Is(err, siteops.ErrOriginNotAllowed):
		c.JSON(http.StatusBadRequest, gin.H{"code": siteops.ErrCodeOriginNotAllowed, "error": siteops.ErrOriginNotAllowed.Error()})
	case errors.Is(err, siteops.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": siteops.SanitizeErrorText(err.Error(), 200)})
	case errors.Is(err, siteops.ErrInstanceNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": siteops.ErrInstanceNotFound.Error()})
	case errors.Is(err, siteops.ErrGenerationMismatch):
		c.JSON(http.StatusConflict, gin.H{"error": siteops.ErrGenerationMismatch.Error()})
	case errors.Is(err, siteops.ErrRateLimited):
		c.JSON(http.StatusTooManyRequests, gin.H{"code": siteops.ErrCodeRateLimited, "error": siteops.ErrRateLimited.Error()})
	case errors.Is(err, siteops.ErrBudgetExceeded):
		c.JSON(http.StatusTooManyRequests, gin.H{"code": siteops.ErrCodeBudgetExceeded, "error": siteops.ErrBudgetExceeded.Error()})
	case errors.Is(err, siteops.ErrQueueTimeout):
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": siteops.ErrCodeQueueTimeout, "error": siteops.ErrQueueTimeout.Error()})
	case errors.Is(err, siteops.ErrUnauthorized):
		c.JSON(http.StatusBadGateway, gin.H{"code": siteops.ErrCodeUnauthorized, "error": siteops.ErrUnauthorized.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": siteops.SanitizeErrorText(err.Error(), 200)})
	}
}
