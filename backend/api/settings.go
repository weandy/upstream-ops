package api

import (
	"net/http"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/gin-gonic/gin"
)

type settingsConfigView struct {
	App           config.AppConfig           `json:"app"`
	Auth          config.AuthConfig          `json:"auth"`
	Scheduler     config.SchedulerConfig     `json:"scheduler"`
	Notifications config.NotificationsConfig `json:"notifications"`
	Proxy         config.ProxyConfig         `json:"proxy"`
	Upstream      config.UpstreamConfig      `json:"upstream"`
	Gateway       config.GatewayConfig       `json:"gateway"`
	Intelligence  config.IntelligenceConfig  `json:"intelligence"`
	Embed         config.EmbedConfig         `json:"embed"`
}

type settingsConfigInput struct {
	App           config.AppConfig           `json:"app" binding:"required"`
	Auth          config.AuthConfig          `json:"auth" binding:"required"`
	Scheduler     config.SchedulerConfig     `json:"scheduler" binding:"required"`
	Notifications config.NotificationsConfig `json:"notifications" binding:"required"`
	Proxy         config.ProxyConfig         `json:"proxy"`
	Upstream      config.UpstreamConfig      `json:"upstream"`
	Gateway       config.GatewayConfig       `json:"gateway"`
	Intelligence  config.IntelligenceConfig  `json:"intelligence"`
	Embed         config.EmbedConfig         `json:"embed"`
}

func registerSettings(g *gin.RouterGroup, d *Deps) {
	gs := g.Group("/settings")
	gs.GET("/config", func(c *gin.Context) { getSettingsConfig(c, d) })
	gs.PUT("/config", func(c *gin.Context) { saveSettingsConfig(c, d) })
	gs.POST("/apply", func(c *gin.Context) { applySettingsConfig(c, d) })
	gs.POST("/proxy/test", func(c *gin.Context) { testProxy(c) })
}

func getSettingsConfig(c *gin.Context, d *Deps) {
	cfg, err := config.LoadFile(d.Runtime.ConfigPath())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"config_path": d.Runtime.ConfigPath(),
			"config": settingsConfigView{
				App:           cfg.App,
				Auth:          cfg.Auth,
				Scheduler:     cfg.Scheduler,
				Notifications: cfg.Notifications,
				Proxy:         cfg.Proxy,
				Upstream:      cfg.Upstream.WithDefaults(),
				Gateway:       cfg.Gateway.WithDefaults(),
				Intelligence:  cfg.Intelligence.WithDefaults(),
				Embed:         cfg.Embed.WithDefaults(),
			},
		},
	})
}

func saveSettingsConfig(c *gin.Context, d *Deps) {
	var in settingsConfigInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}

	path := d.Runtime.ConfigPath()
	cfg, err := config.LoadFile(path)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	cfg.App.Title = in.App.Title
	cfg.App.NotificationPrefix = in.App.NotificationPrefix
	cfg.Auth = in.Auth
	cfg.Scheduler = in.Scheduler
	cfg.Notifications = in.Notifications
	cfg.Proxy = in.Proxy
	cfg.Upstream = in.Upstream.WithDefaults()
	cfg.Gateway = in.Gateway.WithDefaults()
	if in.Intelligence.LMFPDDataDir != "" ||
		in.Intelligence.TickIntervalSeconds > 0 ||
		in.Intelligence.Concurrency > 0 ||
		in.Intelligence.BackoffEnabled != nil ||
		in.Intelligence.BackoffTransientMaxSeconds > 0 ||
		in.Intelligence.BackoffRateLimitMaxSeconds > 0 ||
		in.Intelligence.BackoffAuthMaxSeconds > 0 {
		cfg.Intelligence = in.Intelligence.WithDefaults()
	}
	if in.Embed.CacheTTLSeconds != nil ||
		in.Embed.RateLimitEnabled != nil ||
		in.Embed.RateLimitRPS > 0 ||
		in.Embed.RateLimitBurst > 0 {
		cfg.Embed = in.Embed.WithDefaults()
	}

	if err := config.Save(path, cfg); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"config_path": path,
			"message":     "已写入配置文件",
		},
	})
}

func applySettingsConfig(c *gin.Context, d *Deps) {
	result, err := d.Runtime.ApplyFromFile()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}
