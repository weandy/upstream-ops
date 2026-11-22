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

type settingsSecretsStatus struct {
	AuthPasswordSet    bool `json:"auth_password_set"`
	AuthTokenSecretSet bool `json:"auth_token_secret_set"`
	ProxyPasswordSet   bool `json:"proxy_password_set"`
}

type settingsConfigInput struct {
	App                  config.AppConfig           `json:"app" binding:"required"`
	Auth                 config.AuthConfig          `json:"auth" binding:"required"`
	Scheduler            config.SchedulerConfig     `json:"scheduler" binding:"required"`
	Notifications        config.NotificationsConfig `json:"notifications" binding:"required"`
	Proxy                config.ProxyConfig         `json:"proxy"`
	Upstream             config.UpstreamConfig      `json:"upstream"`
	Gateway              config.GatewayConfig       `json:"gateway"`
	Intelligence         config.IntelligenceConfig  `json:"intelligence"`
	Embed                config.EmbedConfig         `json:"embed"`
	ClearAuthTokenSecret bool                       `json:"clear_auth_token_secret"`
	ClearProxyPassword   bool                       `json:"clear_proxy_password"`
}

func registerSettings(g *gin.RouterGroup, d *Deps) {
	gs := g.Group("/settings")
	gs.GET("/config", func(c *gin.Context) { getSettingsConfig(c, d) })
	gs.PUT("/config", func(c *gin.Context) { saveSettingsConfig(c, d) })
	gs.POST("/apply", func(c *gin.Context) { applySettingsConfig(c, d) })
	gs.POST("/proxy/test", func(c *gin.Context) { testProxyWithDeps(c, d) })
}

func getSettingsConfig(c *gin.Context, d *Deps) {
	cfg, err := config.LoadFile(d.Runtime.ConfigPath())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	secretsStatus := settingsSecretsStatus{
		AuthPasswordSet:    cfg.Auth.Password != "",
		AuthTokenSecretSet: cfg.Auth.TokenSecret != "",
		ProxyPasswordSet:   cfg.Proxy.Password != "",
	}
	redactedAuth := cfg.Auth
	redactedAuth.Password = ""
	redactedAuth.TokenSecret = ""
	redactedProxy := cfg.Proxy
	redactedProxy.Password = ""

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"config_path":    d.Runtime.ConfigPath(),
			"secrets_status": secretsStatus,
			"config": settingsConfigView{
				App:           cfg.App,
				Auth:          redactedAuth,
				Scheduler:     cfg.Scheduler,
				Notifications: cfg.Notifications,
				Proxy:         redactedProxy,
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

	prevAuthPassword := cfg.Auth.Password
	prevAuthTokenSecret := cfg.Auth.TokenSecret
	prevProxyPassword := cfg.Proxy.Password

	cfg.App.Title = in.App.Title
	cfg.App.NotificationPrefix = in.App.NotificationPrefix
	cfg.Auth = in.Auth
	if cfg.Auth.Password == "" {
		cfg.Auth.Password = prevAuthPassword
	}
	if in.ClearAuthTokenSecret {
		cfg.Auth.TokenSecret = ""
	} else if cfg.Auth.TokenSecret == "" {
		cfg.Auth.TokenSecret = prevAuthTokenSecret
	}

	cfg.Scheduler = in.Scheduler
	cfg.Notifications = in.Notifications
	cfg.Proxy = in.Proxy
	if in.ClearProxyPassword {
		cfg.Proxy.Password = ""
	} else if cfg.Proxy.Password == "" {
		cfg.Proxy.Password = prevProxyPassword
	}
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
