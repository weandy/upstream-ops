// Package auth 提供单管理员登录：账号密码写在 config 里，登录后下发 HMAC-SHA256 签名的短 token。
//
// Token 格式："<base64url(payload)>.<base64url(hmac)>"，payload 是 {"sub":"<user>","exp":<unix>}。
// 服务端无状态，AppSecret 不变的情况下重启 token 仍有效。
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Service 单管理员登录服务。
type Service struct {
	username              string
	password              string
	credVer               string
	mustChangeCredentials bool
	secret                []byte
	tokenTTL              time.Duration
}

// New 构造 Service。secret 推荐 32 字节以上；若为空报错。
// 调用方应在 secret 为空时回退到 APP_SECRET。
func New(username, password, secret string, ttl time.Duration) (*Service, error) {
	return NewWithOptions(username, password, secret, ttl, false)
}

// NewWithOptions 构造 Service，并指定是否要求首次登录后强制修改账号密码。
func NewWithOptions(username, password, secret string, ttl time.Duration, mustChangeCredentials bool) (*Service, error) {
	if username == "" || password == "" {
		return nil, errors.New("auth username / password are required")
	}
	if secret == "" {
		return nil, errors.New("auth token secret is empty")
	}
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	sum := sha256.Sum256([]byte("auth-cred:" + username + "\x00" + password))
	return &Service{
		username:              username,
		password:              password,
		credVer:               fmt.Sprintf("%x", sum[:8]),
		mustChangeCredentials: mustChangeCredentials,
		secret:                []byte(secret),
		tokenTTL:              ttl,
	}, nil
}

// claims 是签发到 token payload 里的最小必要字段。
type claims struct {
	Sub string `json:"sub"`
	Exp int64  `json:"exp"`
	Ver string `json:"ver,omitempty"`
}

// Login 校验账号密码，返回新的 token 与过期时间。
func (s *Service) Login(username, password string) (string, time.Time, error) {
	if subtle.ConstantTimeCompare([]byte(username), []byte(s.username)) != 1 ||
		subtle.ConstantTimeCompare([]byte(password), []byte(s.password)) != 1 {
		return "", time.Time{}, errors.New("invalid username or password")
	}
	expiresAt := time.Now().Add(s.tokenTTL)
	c := claims{Sub: s.username, Exp: expiresAt.Unix(), Ver: s.credVer}
	tok, err := s.sign(c)
	if err != nil {
		return "", time.Time{}, err
	}
	return tok, expiresAt, nil
}

// Verify 校验 token 合法性并返回 subject。
func (s *Service) Verify(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", errors.New("malformed token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode sig: %w", err)
	}
	expectedSig := s.mac(payload)
	if subtle.ConstantTimeCompare(sig, expectedSig) != 1 {
		return "", errors.New("bad signature")
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", fmt.Errorf("decode claims: %w", err)
	}
	if time.Now().Unix() > c.Exp {
		return "", errors.New("token expired")
	}
	if c.Sub != s.username {
		return "", errors.New("unknown subject")
	}
	if subtle.ConstantTimeCompare([]byte(c.Ver), []byte(s.credVer)) != 1 {
		return "", errors.New("credentials changed")
	}
	return c.Sub, nil
}

func (s *Service) sign(c claims) (string, error) {
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	sig := s.mac(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (s *Service) mac(payload []byte) []byte {
	m := hmac.New(sha256.New, s.secret)
	m.Write(payload)
	return m.Sum(nil)
}

// Username 返回当前 Service 绑定的管理员账号（前端展示用）。
func (s *Service) Username() string { return s.username }

// MustChangeCredentials 返回当前是否仍处于必须重设账号密码的初始状态。
func (s *Service) MustChangeCredentials() bool { return s.mustChangeCredentials }

// TokenTTL 返回登录 token 的有效期。
func (s *Service) TokenTTL() time.Duration { return s.tokenTTL }

// Middleware 校验 Authorization 头。不通过返回 401；
// 若仍在使用初始凭据，除认证状态与初始化接口外全部返回 403。
//
// 路径白名单（不需要鉴权）：
//   - "/healthz"
//   - "/api/version"
//   - "/api/auth/login"
func (s *Service) Middleware() gin.HandlerFunc {
	whitelist := map[string]struct{}{
		"/healthz":        {},
		"/api/version":    {},
		"/api/auth/login": {},
	}
	setupAllowed := map[string]struct{}{
		"/api/auth/me":     {},
		"/api/auth/logout": {},
		"/api/auth/setup":  {},
	}
	return func(c *gin.Context) {
		if _, ok := whitelist[c.FullPath()]; ok {
			c.Next()
			return
		}
		if _, ok := whitelist[c.Request.URL.Path]; ok {
			c.Next()
			return
		}
		token := extractToken(c.Request)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		sub, err := s.Verify(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		if s.mustChangeCredentials {
			_, okFull := setupAllowed[c.FullPath()]
			_, okPath := setupAllowed[c.Request.URL.Path]
			if !okFull && !okPath {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error": "首次登录必须先重新设置账号和密码",
					"code":  "must_change_credentials",
				})
				return
			}
		}
		c.Set("authSubject", sub)
		c.Next()
	}
}

func extractToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if c, err := r.Cookie("uh_token"); err == nil {
		return c.Value
	}
	return ""
}

const (
	defaultMaxTrackedLoginIPs = 4096
	loginRecordIdleExpiry     = 30 * time.Minute
)

type loginAttemptRecord struct {
	failures    int
	lockedUntil time.Time
	lastSeen    time.Time
}

// LoginRateLimiter 轻量按 IP 登录防爆破限流器，支持失败锁定、成功清零与有界容量防 OOM。
type LoginRateLimiter struct {
	mu           sync.Mutex
	maxFailures  int
	lockDuration time.Duration
	maxTracked   int
	records      map[string]*loginAttemptRecord
	nowFunc      func() time.Time
}

// NewLoginRateLimiter 构造按 IP 防爆破限流器。
func NewLoginRateLimiter(maxFailures int, lockDuration time.Duration, nowFunc func() time.Time) *LoginRateLimiter {
	if maxFailures <= 0 {
		maxFailures = 5
	}
	if lockDuration <= 0 {
		lockDuration = 5 * time.Minute
	}
	if nowFunc == nil {
		nowFunc = func() time.Time { return time.Now().UTC() }
	}
	return &LoginRateLimiter{
		maxFailures:  maxFailures,
		lockDuration: lockDuration,
		maxTracked:   defaultMaxTrackedLoginIPs,
		records:      make(map[string]*loginAttemptRecord),
		nowFunc:      nowFunc,
	}
}

// Allow 判断给定 IP 当前是否允许发起登录尝试；被锁定时返回建议等待秒数（>= 1）。
func (l *LoginRateLimiter) Allow(ip string) (bool, int) {
	if l == nil {
		return true, 0
	}
	key := normalizeLoginIP(ip)
	now := l.nowFunc()
	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.records[key]
	if !ok {
		return true, 0
	}
	rec.lastSeen = now
	if !rec.lockedUntil.IsZero() && now.Before(rec.lockedUntil) {
		sec := int(math.Ceil(rec.lockedUntil.Sub(now).Seconds()))
		if sec < 1 {
			sec = 1
		}
		return false, sec
	}
	if !rec.lockedUntil.IsZero() && !now.Before(rec.lockedUntil) {
		// 锁定期已过，重置计数允许重新尝试。
		rec.failures = 0
		rec.lockedUntil = time.Time{}
	}
	return true, 0
}

// RecordFailure 记录一次登录失败；连续失败达到阈值时启动冷却锁定。
func (l *LoginRateLimiter) RecordFailure(ip string) {
	if l == nil {
		return
	}
	key := normalizeLoginIP(ip)
	now := l.nowFunc()
	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.records[key]
	if !ok {
		if len(l.records) >= l.maxTracked {
			l.evictLocked(now)
		}
		rec = &loginAttemptRecord{}
		l.records[key] = rec
	}
	rec.lastSeen = now
	rec.failures++
	if rec.failures >= l.maxFailures {
		rec.lockedUntil = now.Add(l.lockDuration)
	}
}

// RecordSuccess 登录成功后清零该 IP 的失败计数。
func (l *LoginRateLimiter) RecordSuccess(ip string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.records, normalizeLoginIP(ip))
	l.mu.Unlock()
}

func (l *LoginRateLimiter) evictLocked(now time.Time) {
	for k, rec := range l.records {
		if now.Sub(rec.lastSeen) >= loginRecordIdleExpiry {
			delete(l.records, k)
		}
	}
	if len(l.records) < l.maxTracked {
		return
	}
	var oldestKey string
	var oldestSeen time.Time
	first := true
	for k, rec := range l.records {
		if first || rec.lastSeen.Before(oldestSeen) {
			oldestKey = k
			oldestSeen = rec.lastSeen
			first = false
		}
	}
	if oldestKey != "" {
		delete(l.records, oldestKey)
	}
}

func normalizeLoginIP(ip string) string {
	key := strings.TrimSpace(ip)
	if key == "" {
		return "unknown"
	}
	return key
}
