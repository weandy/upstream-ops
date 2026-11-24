package notify

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SiteOpsSubscription 通知渠道对自有站点运维（siteops）事件的显式订阅配置。
//
// 安全语义（严格 Default Deny / 零广播）：
//   - 老渠道（空串 / null / 未启用）、解析失败的 JSON、空 instance_ids 或空 events 一律拒绝；
//   - 只有显式设置 enabled=true 且 instance_ids、events 均命中（支持显式 "*"）才允许直投。
type SiteOpsSubscription struct {
	Enabled     bool     `json:"enabled"`
	InstanceIDs []string `json:"instance_ids"`
	Events      []string `json:"events"`
}

// UnmarshalJSON 兼容字符串数组（["*", "1"]）与整数数组（[1, 2]）形式的 instance_ids。
func (s *SiteOpsSubscription) UnmarshalJSON(data []byte) error {
	var raw struct {
		Enabled     bool              `json:"enabled"`
		InstanceIDs []json.RawMessage `json:"instance_ids"`
		Events      []string          `json:"events"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	ids := make([]string, 0, len(raw.InstanceIDs))
	for _, item := range raw.InstanceIDs {
		trimmed := strings.TrimSpace(string(item))
		if trimmed == "" || trimmed == "null" {
			continue
		}
		var strVal string
		if err := json.Unmarshal(item, &strVal); err == nil {
			strVal = strings.TrimSpace(strVal)
			if strVal != "" {
				ids = append(ids, strVal)
			}
			continue
		}
		var uintVal uint64
		if err := json.Unmarshal(item, &uintVal); err == nil && uintVal > 0 {
			ids = append(ids, strconv.FormatUint(uintVal, 10))
			continue
		}
		return fmt.Errorf("invalid site_ops instance_id entry: %s", trimmed)
	}
	events := make([]string, 0, len(raw.Events))
	for _, ev := range raw.Events {
		ev = strings.TrimSpace(ev)
		if ev != "" {
			events = append(events, ev)
		}
	}
	s.Enabled = raw.Enabled
	s.InstanceIDs = ids
	s.Events = events
	return nil
}

// ParseSiteOpsSubscription 严格解析 site_ops_subscriptions JSON。
// 空串或 null 返回禁用态且无错误；非法 JSON 返回禁用态与解析错误（调用方必须视为拒绝）。
func ParseSiteOpsSubscription(raw string) (SiteOpsSubscription, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return SiteOpsSubscription{}, nil
	}
	var sub SiteOpsSubscription
	if err := json.Unmarshal([]byte(trimmed), &sub); err != nil {
		return SiteOpsSubscription{}, err
	}
	return sub, nil
}

// NormalizeSiteOpsSubscription 校验并规整通知渠道保存时的 site_ops_subscriptions 字符串。
// 空串或 null 规整为空串；非法 JSON 或非法 instance_id 返回错误供 API 返回 400。
func NormalizeSiteOpsSubscription(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}
	sub, err := ParseSiteOpsSubscription(trimmed)
	if err != nil {
		return "", err
	}
	seenIDs := make(map[string]struct{}, len(sub.InstanceIDs))
	cleanIDs := make([]string, 0, len(sub.InstanceIDs))
	for _, id := range sub.InstanceIDs {
		if id != "*" {
			n, parseErr := strconv.ParseUint(id, 10, 64)
			if parseErr != nil || n == 0 {
				return "", fmt.Errorf("invalid site_ops instance_id %q", id)
			}
			id = strconv.FormatUint(n, 10)
		}
		if _, exists := seenIDs[id]; !exists {
			seenIDs[id] = struct{}{}
			cleanIDs = append(cleanIDs, id)
		}
	}
	seenEvs := make(map[string]struct{}, len(sub.Events))
	cleanEvs := make([]string, 0, len(sub.Events))
	for _, ev := range sub.Events {
		if _, exists := seenEvs[ev]; !exists {
			seenEvs[ev] = struct{}{}
			cleanEvs = append(cleanEvs, ev)
		}
	}
	sub.InstanceIDs = cleanIDs
	sub.Events = cleanEvs
	out, err := json.Marshal(sub)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Matches 严格判定该渠道是否显式订阅指定实例与事件键（Default Deny）。
func (s SiteOpsSubscription) Matches(instanceID uint, eventKey string) bool {
	if !s.Enabled || instanceID == 0 {
		return false
	}
	ev := strings.TrimSpace(eventKey)
	if ev == "" || len(s.InstanceIDs) == 0 || len(s.Events) == 0 {
		return false
	}
	idStr := strconv.FormatUint(uint64(instanceID), 10)
	instMatched := false
	for _, target := range s.InstanceIDs {
		if target == "*" || target == idStr {
			instMatched = true
			break
		}
	}
	if !instMatched {
		return false
	}
	for _, allowed := range s.Events {
		if allowed == "*" || allowed == ev {
			return true
		}
	}
	return false
}

// MatchesSiteOpsChannel 从原始 JSON 字符串判定是否放行；解析失败一律返回 false。
func MatchesSiteOpsChannel(raw string, instanceID uint, eventKey string) bool {
	sub, err := ParseSiteOpsSubscription(raw)
	if err != nil {
		return false
	}
	return sub.Matches(instanceID, eventKey)
}
