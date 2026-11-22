package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/bejix/upstream-ops/backend/auth"
	"github.com/gin-gonic/gin"
)

func registerAuth(g *gin.RouterGroup, d *Deps) {
	loginLimiter := auth.NewLoginRateLimiter(5, 5*time.Minute, nil)
	g.POST("/auth/login", func(c *gin.Context) { login(c, d, loginLimiter) })
	g.GET("/auth/me", func(c *gin.Context) { whoami(c, d) })
	g.POST("/auth/logout", func(c *gin.Context) {
		// 无状态 token，客户端丢弃即可；这个接口仅作语义存在。
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
}

type loginInput struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func login(c *gin.Context, d *Deps, limiter *auth.LoginRateLimiter) {
	// 鉴权关闭：任何登录请求都直接成功；前端在 /auth/me 已经知道无需登录。
	authSvc := d.Runtime.CurrentAuth()
	if authSvc == nil {
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"auth_disabled": true,
				"username":      "anonymous",
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
			"token":      token,
			"expires_at": exp.Unix(),
			"username":   authSvc.Username(),
		},
	})
}

// whoami 既是"前端启动探测"接口也是"已登录信息"接口。
//
//   - 鉴权关闭 → 返回 {auth_disabled: true}，前端据此跳过登录页
//   - 鉴权开启但未带 token → 中间件已经在前面 401 拦截，根本走不到这里
//   - 鉴权开启 + 有效 token → 返回 username
func whoami(c *gin.Context, d *Deps) {
	if d.Runtime.CurrentAuth() == nil {
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"auth_disabled": true,
				"username":      "anonymous",
			},
		})
		return
	}
	sub, _ := c.Get("authSubject")
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"username": sub}})
}
