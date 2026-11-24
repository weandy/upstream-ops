package siteops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func MaskEmailStrict(raw string) *string {
	raw = strings.TrimSpace(raw)
	if strings.IndexFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) || r == '<' || r == '>' }) >= 0 || strings.Count(raw, "@") != 1 {
		return nil
	}
	local, domain, _ := strings.Cut(raw, "@")
	domain = strings.ToLower(domain)
	if local == "" || len(domain) > 253 || !strings.Contains(domain, ".") {
		return nil
	}
	for _, pre := range []string{"sk-", "adm_", "admin-"} {
		if strings.Contains(strings.ToLower(local), pre) {
			return nil
		}
	}
	for _, part := range strings.Split(domain, ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return nil
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return nil
			}
		}
	}
	r := []rune(local)
	masked := "***"
	if len(r) >= 5 {
		masked = string(r[:2]) + masked + string(r[len(r)-1:])
	} else if len(r) > 1 {
		masked = string(r[:1]) + masked
	}
	return ptr(masked + "@" + domain)
}
func SanitizeSafeLabel(raw string, n int, fallback string) string {
	if n <= 0 {
		n = 128
	}
	clean := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, raw)), " ")
	low := strings.ToLower(clean)
	if clean == "" || strings.Contains(clean, "@") || reportEmailPattern.MatchString(clean) || strings.Contains(low, "http://") || strings.Contains(low, "https://") || strings.Contains(SanitizeErrorText(clean, len(clean)+1), "[redacted]") {
		return fallback
	}
	r := []rune(clean)
	return string(r[:min(n, len(r))])
}
func enum(raw string, values ...string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if slices.Contains(values, v) {
		return v
	}
	return ""
}
func platform(raw string) string {
	return enum(raw, "openai", "anthropic", "gemini", "antigravity", "bedrock", "claude", "vertex", "azure", "openai_compatible")
}
func cleanGroup(g SanitizedGroupInfo) SanitizedGroupInfo {
	g.Name = SanitizeSafeLabel(g.Name, 128, fmt.Sprintf("Group #%d", g.ID))
	g.Status = enum(g.Status, "active", "inactive", "disabled")
	g.Platform = platform(g.Platform)
	if g.SubscriptionType != nil {
		v := enum(*g.SubscriptionType, "standard", "subscription", "balance")
		g.SubscriptionType = nil
		if v != "" {
			g.SubscriptionType = &v
		}
	}
	return g
}

type pageHeader struct {
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	Total    *int64 `json:"total"`
	HasMore  *bool  `json:"has_more"`
}

func containerData(raw []byte, keys []string) ([]byte, pageHeader, error) {
	data, err := decodeEnvelopeData(raw)
	var h pageHeader
	if err != nil {
		return nil, h, err
	}
	if data[0] == '[' {
		return data, h, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &h) != nil || json.Unmarshal(data, &fields) != nil {
		return nil, h, errInvalidPayload
	}
	for _, key := range keys {
		if b := bytes.TrimSpace(fields[key]); len(b) > 0 && (b[0] == '[' || b[0] == '{') {
			return b, h, nil
		}
	}
	return nil, h, errInvalidPayload
}
func projectPage[W, T any](raw []byte, limit int, keys []string, project func(W) (T, bool)) (AnalyticsPage[T], error) {
	return projectDirectoryPage(raw, 1, limit, keys, project)
}
func projectDirectoryPage[W, T any](raw []byte, requestedPage, limit int, keys []string, project func(W) (T, bool)) (AnalyticsPage[T], error) {
	data, h, err := containerData(raw, keys)
	if h.Page < 1 || h.Page > 10000 {
		h.Page = requestedPage
	}
	effectiveSize := limit
	if h.PageSize > 0 && h.PageSize < limit {
		effectiveSize = h.PageSize
	}
	out := AnalyticsPage[T]{Page: h.Page, PageSize: effectiveSize, Total: h.Total, HasMore: h.HasMore, Items: []T{}}
	if err != nil {
		return out, err
	}
	var rows []W
	if json.Unmarshal(data, &rows) != nil || rows == nil {
		return out, errInvalidPayload
	}
	out.Truncated = len(rows) > limit
	for _, v := range rows[:min(len(rows), limit)] {
		if row, ok := project(v); ok {
			out.Items = append(out.Items, row)
		} else {
			out.Truncated = true
		}
	}
	if out.HasMore == nil {
		if out.Total != nil {
			page := max(1, out.Page)
			out.HasMore = ptr(int64(page-1)*int64(effectiveSize)+int64(len(rows)) < *out.Total)
		} else if !out.Truncated && len(rows) < effectiveSize {
			out.HasMore = ptr(false)
		}
	}
	return out, nil
}

type userWire struct {
	ID          int64      `json:"id"`
	Email       string     `json:"email"`
	Status      string     `json:"status"`
	Role        string     `json:"role"`
	CreatedAt   *time.Time `json:"created_at"`
	Balance     *float64   `json:"balance"`
	Concurrency *int       `json:"concurrency"`
}

func ParseUsersPage(raw []byte) (AnalyticsPage[SanitizedAnalyticsUser], error) {
	return parseUsersPage(raw, 1)
}
func parseUsersPage(raw []byte, page int) (AnalyticsPage[SanitizedAnalyticsUser], error) {
	return projectDirectoryPage(raw, page, 100, []string{"items", "users", "rows", "list"}, func(w userWire) (SanitizedAnalyticsUser, bool) {
		return SanitizedAnalyticsUser{w.ID, MaskEmailStrict(w.Email), enum(w.Role, "admin", "user", "member"), enum(w.Status, "active", "disabled", "banned", "inactive"), w.CreatedAt, w.Balance, w.Concurrency}, w.ID > 0
	})
}

type platformUsageWire struct {
	Platform string   `json:"platform"`
	Today    *float64 `json:"today_actual_cost"`
	Total    *float64 `json:"total_actual_cost"`
}

type quickWire struct {
	UserID    int64               `json:"user_id"`
	KeyID     int64               `json:"api_key_id"`
	Today     *float64            `json:"today_actual_cost"`
	Total     *float64            `json:"total_actual_cost"`
	Platforms []platformUsageWire `json:"by_platform"`
}

func projectIDs[W, T any](raw []byte, limit int, keys []string, project func(W) (int64, T)) (map[int64]T, error) {
	data, h, err := containerData(raw, keys)
	_ = h
	if err != nil {
		data, err = decodeEnvelopeData(raw)
		if err != nil {
			return nil, err
		}
	}
	out := make(map[int64]T)
	if data[0] == '[' {
		var rows []W
		if json.Unmarshal(data, &rows) != nil {
			return nil, errInvalidPayload
		}
		for _, v := range rows[:min(limit, len(rows))] {
			id, row := project(v)
			if id > 0 {
				out[id] = row
			}
		}
		return out, nil
	}
	var rows map[string]W
	if json.Unmarshal(data, &rows) != nil || rows == nil {
		return nil, errInvalidPayload
	}
	ids := make([]int64, 0, len(rows))
	for k := range rows {
		id, e := strconv.ParseInt(k, 10, 64)
		if e != nil || id <= 0 || strconv.FormatInt(id, 10) != k {
			return nil, errInvalidPayload
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids[:min(limit, len(ids))] {
		inside, row := project(rows[strconv.FormatInt(id, 10)])
		if inside == 0 || inside == id {
			out[id] = row
		}
	}
	return out, nil
}
func ParseUsersUsageBatch(raw []byte) (map[int64]SanitizedUserQuickUsage, error) {
	return projectIDs(raw, 100, []string{"stats", "users", "usage", "items"}, func(w quickWire) (int64, SanitizedUserQuickUsage) {
		v := SanitizedUserQuickUsage{Today: w.Today, Recent: w.Total}
		for _, p := range w.Platforms {
			if name := platform(p.Platform); name != "" {
				v.ByPlatform = append(v.ByPlatform, PlatformCostBrief{name, p.Today, p.Total})
			}
		}
		return w.UserID, v
	})
}
func ParseAPIKeysUsageBatch(raw []byte) (map[int64]SanitizedAPIKeyUsage, error) {
	return projectIDs(raw, 20, []string{"stats", "api_keys", "usage", "items"}, func(w quickWire) (int64, SanitizedAPIKeyUsage) {
		return w.KeyID, SanitizedAPIKeyUsage{w.Today, w.Total}
	})
}
func ParseUserConcurrency(raw []byte) (SanitizedUserConcurrencyMap, error) {
	type wire struct {
		UserID int64 `json:"user_id"`
		UserConcurrencyView
	}
	return projectIDs(raw, 1000, []string{"user", "users", "items"}, func(w wire) (int64, UserConcurrencyView) { return w.UserID, w.UserConcurrencyView })
}

type subWire struct {
	ID           int64              `json:"id"`
	UserID       int64              `json:"user_id"`
	GroupID      int64              `json:"group_id"`
	Status       string             `json:"status"`
	StartsAt     *time.Time         `json:"starts_at"`
	ExpiresAt    *time.Time         `json:"expires_at"`
	RevokedAt    *time.Time         `json:"revoked_at"`
	Group        SanitizedGroupInfo `json:"group"`
	User         userWire           `json:"user"`
	Daily        *float64           `json:"daily_usage_usd"`
	Weekly       *float64           `json:"weekly_usage_usd"`
	Monthly      *float64           `json:"monthly_usage_usd"`
	DailyStart   *time.Time         `json:"daily_window_start"`
	WeeklyStart  *time.Time         `json:"weekly_window_start"`
	MonthlyStart *time.Time         `json:"monthly_window_start"`
}

func listWindow(limit, used *float64, start *time.Time) RollingWindowView {
	w := RollingWindowView{LimitUSD: limit, UsedUSD: used, WindowStart: start, Source: "list_snapshot", Quality: QualityUnavailable}
	if used != nil {
		w.Quality = QualityTrusted
	}
	return w
}
func ParseSubscriptionsPage(raw []byte) (AnalyticsPage[SanitizedSubscription], error) {
	return parseSubscriptionsPage(raw, 1)
}
func parseSubscriptionsPage(raw []byte, page int) (AnalyticsPage[SanitizedSubscription], error) {
	return projectDirectoryPage(raw, page, 100, []string{"items", "subscriptions", "rows", "list"}, func(w subWire) (SanitizedSubscription, bool) {
		w.Group.ID = w.GroupID
		g := cleanGroup(w.Group)
		return SanitizedSubscription{w.ID, w.UserID, w.GroupID, MaskEmailStrict(w.User.Email), enum(w.Status, "active", "expired", "revoked", "cancelled", "inactive"), w.StartsAt, w.ExpiresAt, w.RevokedAt, g, listWindow(g.DailyLimitUSD, w.Daily, w.DailyStart), listWindow(g.WeeklyLimitUSD, w.Weekly, w.WeeklyStart), listWindow(g.MonthlyLimitUSD, w.Monthly, w.MonthlyStart)}, w.ID > 0 && w.UserID > 0 && w.GroupID > 0
	})
}

type rollingWire struct {
	LimitUSD        *float64   `json:"limit_usd"`
	UsedUSD         *float64   `json:"used_usd"`
	RemainingUSD    *float64   `json:"remaining_usd"`
	Percentage      *float64   `json:"percentage"`
	WindowStart     *time.Time `json:"window_start"`
	ResetsAt        *time.Time `json:"resets_at"`
	ResetsInSeconds *int64     `json:"resets_in_seconds"`
}

func (w *rollingWire) view() *RollingWindowView {
	if w == nil {
		return nil
	}
	return &RollingWindowView{w.LimitUSD, w.UsedUSD, w.RemainingUSD, w.Percentage, w.WindowStart, w.ResetsAt, w.ResetsInSeconds, "progress", QualityTrusted}
}
func ParseSubscriptionProgress(raw []byte) (*SanitizedSubscriptionProgress, error) {
	var w struct {
		ID                     int64
		Daily, Weekly, Monthly *rollingWire
		ExpiresAt              *time.Time `json:"expires_at"`
	}
	if err := decodeProjection(raw, &w); err != nil {
		return nil, err
	}
	if w.ID <= 0 && w.ExpiresAt == nil && w.Daily == nil && w.Weekly == nil && w.Monthly == nil {
		return nil, errInvalidPayload
	}
	return &SanitizedSubscriptionProgress{w.Daily.view(), w.Weekly.view(), w.Monthly.view(), w.ExpiresAt}, nil
}

type statsWire struct {
	Requests *int64   `json:"total_requests"`
	Input    *int64   `json:"total_input_tokens"`
	Output   *int64   `json:"total_output_tokens"`
	Write    *int64   `json:"total_cache_creation_tokens"`
	Read     *int64   `json:"total_cache_read_tokens"`
	Total    *int64   `json:"total_tokens"`
	Cost     *float64 `json:"total_cost"`
	Actual   *float64 `json:"total_actual_cost"`
	Account  *float64 `json:"total_account_cost"`
	Duration *float64 `json:"average_duration_ms"`
}

func ParseUsageStats(raw []byte) (*SanitizedUsageStats, error) {
	var w statsWire
	if err := decodeProjection(raw, &w); err != nil {
		return nil, err
	}
	if w == (statsWire{}) {
		return nil, errInvalidPayload
	}
	return &SanitizedUsageStats{w.Requests, w.Cost, w.Actual, w.Account, w.Duration, cacheMetrics(w.Input, w.Output, w.Write, w.Read, w.Total)}, nil
}

type keyWire struct {
	ID         int64              `json:"id"`
	GroupID    *int64             `json:"group_id"`
	Group      SanitizedGroupInfo `json:"group"`
	Status     string             `json:"status"`
	CreatedAt  *time.Time         `json:"created_at"`
	LastUsedAt *time.Time         `json:"last_used_at"`
}

func ParseUserAPIKeysPage(raw []byte) (AnalyticsPage[SanitizedUserAPIKey], error) {
	return parseUserAPIKeysPage(raw, 1)
}
func parseUserAPIKeysPage(raw []byte, page int) (AnalyticsPage[SanitizedUserAPIKey], error) {
	return projectDirectoryPage(raw, page, 20, []string{"items", "api_keys", "keys", "rows", "list"}, func(w keyWire) (SanitizedUserAPIKey, bool) {
		v := SanitizedUserAPIKey{APIKeyID: w.ID, GroupID: w.GroupID, Status: enum(w.Status, "active", "disabled", "inactive", "expired", "quota_exhausted"), CreatedAt: w.CreatedAt, LastUsedAt: w.LastUsedAt}
		if w.GroupID != nil {
			w.Group.ID = *w.GroupID
			v.GroupName = cleanGroup(w.Group).Name
		}
		return v, w.ID > 0
	})
}

type marketWire struct {
	Model     string   `json:"model"`
	Requested string   `json:"requested_model"`
	Upstream  string   `json:"upstream_model"`
	GroupID   int64    `json:"group_id"`
	GroupName string   `json:"group_name"`
	Requests  *int64   `json:"requests"`
	Input     *int64   `json:"input_tokens"`
	Output    *int64   `json:"output_tokens"`
	Write     *int64   `json:"cache_creation_tokens"`
	Read      *int64   `json:"cache_read_tokens"`
	Total     *int64   `json:"total_tokens"`
	Cost      *float64 `json:"cost"`
	Actual    *float64 `json:"actual_cost"`
	Account   *float64 `json:"account_cost"`
}

func ParseDashboardModels(raw []byte) (SanitizedModelRows, error) {
	p, err := projectPage(raw, 500, []string{"models", "items", "stats", "rows"}, func(w marketWire) (SanitizedModelRow, bool) {
		if w.Requested != "" {
			w.Model = w.Requested
		}
		return SanitizedModelRow{SanitizeSafeLabel(w.Model, 128, "unknown_model"), SanitizeSafeLabel(w.Upstream, 128, ""), SanitizedUsageStats{w.Requests, w.Cost, w.Actual, w.Account, nil, cacheMetrics(w.Input, w.Output, w.Write, w.Read, w.Total)}}, true
	})
	return SanitizedModelRows{p.Items, p.Truncated}, err
}
func ParseDashboardGroups(raw []byte) (SanitizedGroupMarketRows, error) {
	p, err := projectPage(raw, 500, []string{"groups", "items", "stats", "rows"}, func(w marketWire) (AnalyticsGroupMarketRowView, bool) {
		return AnalyticsGroupMarketRowView{GroupID: w.GroupID, GroupName: SanitizeSafeLabel(w.GroupName, 128, fmt.Sprintf("Group #%d", w.GroupID)), Requests: w.Requests, StandardUSD: w.Cost, ActualUSD: w.Actual, AccountUSD: w.Account, HasCostSpread: costSpread(w.Actual, w.Account), Quality: QualityTrusted}, w.GroupID > 0
	})
	return SanitizedGroupMarketRows{p.Items, p.Truncated}, err
}
func ParseRiskControlStatus(raw []byte) (*SanitizedRiskControlStatus, error) {
	var w SanitizedRiskControlStatus
	if err := decodeProjection(raw, &w); err != nil {
		return nil, err
	}
	if w.Enabled == nil && w.RiskControlEnabled == nil {
		return nil, errInvalidPayload
	}
	w.Mode = enum(w.Mode, "monitor", "block", "audit", "pre_block", "observe")
	w.Engine = enum(w.Engine, "openai", "local", "builtin", "moderation")
	return &w, nil
}
func ParseBackupsList(raw []byte) (SanitizedBackupsResult, error) {
	p, err := projectPage(raw, 500, []string{"items", "backups", "records", "rows", "list"}, func(w SanitizedBackup) (SanitizedBackup, bool) {
		w.Status = enum(w.Status, "completed", "failed", "running", "in_progress", "pending", "cancelled")
		w.BackupType = enum(w.BackupType, "postgres", "postgresql", "full", "incremental", "manual", "scheduled")
		return w, true
	})
	sort.SliceStable(p.Items, func(i, j int) bool {
		a, b := p.Items[i], p.Items[j]
		if a.StartedAt != nil && b.StartedAt != nil && !a.StartedAt.Equal(*b.StartedAt) {
			return a.StartedAt.After(*b.StartedAt)
		}
		if a.StartedAt == nil || b.StartedAt == nil {
			return a.StartedAt != nil
		}
		return a.FinishedAt != nil && (b.FinishedAt == nil || a.FinishedAt.After(*b.FinishedAt))
	})
	return SanitizedBackupsResult{p.Items, p.Truncated || p.HasMore != nil && *p.HasMore || p.Total != nil && *p.Total > int64(len(p.Items))}, err
}
func ParseChannelMonitorAvailability(raw []byte) (*SanitizedChannelAvailability, error) {
	data, err := decodeEnvelopeData(raw)
	if err != nil || !json.Valid(data) || (data[0] != '{' && data[0] != '[') {
		return nil, errInvalidPayload
	}
	return &SanitizedChannelAvailability{true}, nil
}
