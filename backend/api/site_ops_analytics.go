package api

import (
	"bytes"
	"encoding/json"
	"github.com/bejix/upstream-ops/backend/siteops"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const maxAnalyticsID = 9007199254740991

func analyticsNumber(s string, max int64) (int64, bool) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0 && n <= max
}
func analyticsBody(c *gin.Context, limit int64, dst any) bool {
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(dst) == nil && dec.Decode(new(any)) == io.EOF
}

type analyticsParams struct {
	id                   uint
	resource, generation int64
	page, keyPage, top   int
	group                *int64
	scope, status, span  string
	force, only          bool
}

func parseAnalytics(c *gin.Context, allowed []string) (analyticsParams, bool) {
	p := analyticsParams{page: 1, keyPage: 1, top: 5, status: "active", span: "30d"}
	id, ok := analyticsNumber(c.Param("id"), maxAnalyticsID)
	if !ok || int64(uint(id)) != id {
		return p, false
	}
	p.id = uint(id)
	for _, k := range []string{"user_id", "subscription_id", "scope_id"} {
		if s := c.Param(k); s != "" {
			p.resource, ok = analyticsNumber(s, maxAnalyticsID)
			if !ok {
				return p, false
			}
		}
	}
	p.scope = c.Param("scope")
	if p.scope != "" && p.scope != "group" && p.scope != "subscription" {
		return p, false
	}
	vals, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return p, false
	}
	for k, list := range vals {
		if !slices.Contains(allowed, k) || len(list) != 1 {
			return p, false
		}
		s := list[0]
		switch k {
		case "force", "cache_only":
			if s != "0" && s != "1" {
				return p, false
			}
			if k == "force" {
				p.force = s == "1"
			} else {
				p.only = s == "1"
			}
		case "page", "user_page", "subscription_page", "key_page", "top_n", "generation", "group_id":
			limit := int64(10000)
			switch k {
			case "top_n":
				limit = 20
			case "generation", "group_id":
				limit = maxAnalyticsID
			}
			v, ok := analyticsNumber(s, limit)
			if !ok {
				return p, false
			}
			switch k {
			case "top_n":
				p.top = int(v)
			case "key_page":
				p.keyPage = int(v)
			case "generation":
				p.generation = v
			case "group_id":
				p.group = &v
			default:
				p.page = int(v)
			}
		case "status":
			if !slices.Contains([]string{"active", "expired", "all"}, s) {
				return p, false
			}
			p.status = s
		case "range":
			if s != "30d" && s != "7d" {
				return p, false
			}
			p.span = s
		}
	}
	return p, !slices.Contains(allowed, "generation") || p.generation > 0
}
func registerSiteOpsAnalytics(sg *gin.RouterGroup, d *Deps) {
	svc := d.SiteOps
	g := sg.Group("/instances/:id/analytics")
	g.Use(func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	register := func(method, path, query string, handler func(*gin.Context, analyticsParams) (any, error)) {
		g.Handle(method, path, func(c *gin.Context) {
			p, ok := parseAnalytics(c, strings.Fields(query))
			if !ok {
				failSiteOps(c, siteops.ErrInvalidInput)
				return
			}
			v, err := handler(c, p)
			if err != nil {
				failSiteOps(c, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"data": v})
		})
	}
	register("GET", "/lights", "force", func(c *gin.Context, p analyticsParams) (any, error) {
		return svc.GetAnalyticsLights(c.Request.Context(), p.id, p.force)
	})
	register("GET", "/channel-status", "force", func(c *gin.Context, p analyticsParams) (any, error) {
		return svc.GetAnalyticsChannelStatus(c.Request.Context(), p.id, p.force)
	})
	register("GET", "/users", "page top_n force cache_only", func(c *gin.Context, p analyticsParams) (any, error) {
		return svc.GetAnalyticsUsers(c.Request.Context(), p.id, siteops.AnalyticsUsersQuery{Page: p.page, TopN: p.top, Force: p.force, CacheOnly: p.only})
	})
	register("GET", "/users/:user_id", "user_page key_page force", func(c *gin.Context, p analyticsParams) (any, error) {
		return svc.GetAnalyticsUserDetail(c.Request.Context(), p.id, p.resource, p.page, p.keyPage, p.force)
	})
	register("GET", "/subscriptions", "page status group_id force cache_only", func(c *gin.Context, p analyticsParams) (any, error) {
		return svc.GetAnalyticsSubscriptions(c.Request.Context(), p.id, siteops.AnalyticsSubscriptionsQuery{Page: p.page, Status: p.status, GroupID: p.group, Force: p.force, CacheOnly: p.only})
	})
	register("GET", "/subscriptions/:subscription_id", "subscription_page force cache_only", func(c *gin.Context, p analyticsParams) (any, error) {
		return svc.GetAnalyticsSubscriptionDetail(c.Request.Context(), p.id, p.resource, p.page, p.force, p.only)
	})
	register("GET", "/market", "range force cache_only", func(c *gin.Context, p analyticsParams) (any, error) {
		return svc.GetAnalyticsMarket(c.Request.Context(), p.id, p.span, p.force, p.only)
	})
	register("GET", "/pricing", "", func(c *gin.Context, p analyticsParams) (any, error) {
		return svc.GetAnalyticsPricing(c.Request.Context(), p.id)
	})
	register("PUT", "/pricing/config", "", func(c *gin.Context, p analyticsParams) (any, error) {
		var b struct {
			FX     *float64 `json:"cny_per_usd"`
			Warn   *float64 `json:"leverage_warn_threshold"`
			Danger *float64 `json:"leverage_danger_threshold"`
		}
		if !analyticsBody(c, 2048, &b) || b.FX == nil || b.Warn == nil || b.Danger == nil {
			return nil, siteops.ErrInvalidInput
		}
		return svc.UpdateAnalyticsConfig(c.Request.Context(), p.id, siteops.AnalyticsConfig{CNYPerUSD: *b.FX, LeverageWarnThreshold: *b.Warn, LeverageDangerThreshold: *b.Danger})
	})
	register("PUT", "/reference-prices/:scope/:scope_id", "", func(c *gin.Context, p analyticsParams) (any, error) {
		var b siteops.PutReferencePriceInput
		if !analyticsBody(c, 1024, &b) {
			return nil, siteops.ErrInvalidInput
		}
		return svc.PutReferencePrice(c.Request.Context(), p.id, p.scope, p.resource, b)
	})
	register("DELETE", "/reference-prices/:scope/:scope_id", "generation", func(c *gin.Context, p analyticsParams) (any, error) {
		err := svc.ClearReferencePrice(c.Request.Context(), p.id, p.scope, p.resource, p.generation)
		return gin.H{"deleted": true}, err
	})
}
