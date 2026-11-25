package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/auth"
	"github.com/bejix/upstream-ops/backend/config"
	"github.com/gin-gonic/gin"
)

func registerAuth(g *gin.RouterGroup, d *Deps) {
	loginLimiter := auth.NewLoginRateLimiter(5, 5*time.Minute, nil)
	g.POST("/auth/login", func(c *gin.Context) { login(c, d, loginLimiter) })
	g.GET("/auth/me", func(c *gin.Context) { whoami(c, d) })
	g.POST("/auth/setup", func(c *gin.Context) { setupCredentials(c, d) })
	g.POST("/auth/logout", func(c *gin.Context) {
		// 无状态 token，客户端丢弃即可；这个接口仅作语义存在。
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
}

type loginInput struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type setupCredentialsInput struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func login(c *gin.Context, d *Deps, limiter *auth.LoginRateLimiter) {
	// 鉴权关闭：任何登录请求都直接成功；前端在 /auth/me 已经知道无需登录。
	authSvc := d.Runtime.CurrentAuth()
	if authSvc == nil {
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"auth_disabled":           true,
				"username":                "anonymous",
				"must_change_credentials": false,
			},
		})
		return
	}
	clientIP := c.ClientIP()
	if ok, retryAfter := limiter.Allow(clientIP); !ok {
		c.Header("Retry-After", strconv.Itoa(retryAfter))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":               "登录失败次数过多，请稍后再试",
			"retry_after_seconds": retryAfter,
		})
		return
	}
	var in loginInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	token, exp, err := authSvc.Login(in.Username, in.Password)
	if err != nil {
		limiter.RecordFailure(clientIP)
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	limiter.RecordSuccess(clientIP)
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"token":                   token,
			"expires_at":              exp.Unix(),
			"username":                authSvc.Username(),
			"must_change_credentials": authSvc.MustChangeCredentials(),
		},
	})
}

// setupCredentials 用于首次使用默认凭据登录后强制重设管理员账号和密码。
func setupCredentials(c *gin.Context, d *Deps) {
	if d.Runtime == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "runtime manager not configured"})
		return
	}
	authSvc := d.Runtime.CurrentAuth()
	if authSvc == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "鉴权未开启"})
		return
	}
	if !authSvc.MustChangeCredentials() {
		c.JSON(http.StatusConflict, gin.H{"error": "当前账号密码已完成初始化，请在系统设置中修改"})
		return
	}
	var in setupCredentialsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	username := strings.TrimSpace(in.Username)
	if username == "" || len([]rune(username)) > 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "账号不能为空且长度不能超过 64 位"})
		return
	}
	if strings.TrimSpace(in.Password) == "" || len(in.Password) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "新密码长度至少 6 位"})
		return
	}
	if in.Password == config.DefaultAuthPassword {
		c.JSON(http.StatusBadRequest, gin.H{"error": "新密码不能继续使用默认密码"})
		return
	}

	newSvc, err := d.Runtime.UpdateAuthCredentials(username, in.Password)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	token, exp, err := newSvc.Login(username, in.Password)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"token":                   token,
			"expires_at":              exp.Unix(),
			"username":                newSvc.Username(),
			"must_change_credentials": newSvc.MustChangeCredentials(),
		},
	})
}

// whoami 既是"前端启动探测"接口也是"已登录信息"接口。
//
//   - 鉴权关闭 → 返回 {auth_disabled: true}，前端据此跳过登录页
//   - 鉴权开启但未带 token → 中间件已经在前面 401 拦截，根本走不到这里
//   - 鉴权开启 + 有效 token → 返回 username 与是否需要重设初始凭据
func whoami(c *gin.Context, d *Deps) {
	authSvc := d.Runtime.CurrentAuth()
	if authSvc == nil {
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"auth_disabled":           true,
				"username":                "anonymous",
				"must_change_credentials": false,
			},
		})
		return
	}
	sub, _ := c.Get("authSubject")
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"username":                sub,
			"must_change_credentials": authSvc.MustChangeCredentials(),
		},
	})
}
