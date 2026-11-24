package siteops

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/notify"
	"github.com/bejix/upstream-ops/backend/storage"
)

const (
	defaultAlertMaxAttempts = 5
	defaultDueDeliveryLimit = 50
)

var defaultRetryBackoffs = []time.Duration{
	30 * time.Second,
	60 * time.Second,
	120 * time.Second,
	300 * time.Second,
}

// AlertDispatcher 负责将站点运维事件跃迁与日报按显式订阅零广播直投至目标通知渠道，
// 并将冷却占位与按目的地失败退避持久化，支持进程重启后仅补发未成功渠道。
type AlertDispatcher struct {
	store       EventStore
	notifier    DirectNotifier
	Now         func() time.Time
	MaxAttempts int
	Backoffs    []time.Duration
}

func NewAlertDispatcher(store EventStore, notifier DirectNotifier) *AlertDispatcher {
	return &AlertDispatcher{
		store:       store,
		notifier:    notifier,
		Now:         func() time.Time { return time.Now().UTC() },
		MaxAttempts: defaultAlertMaxAttempts,
		Backoffs:    append([]time.Duration(nil), defaultRetryBackoffs...),
	}
}

func (d *AlertDispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

func (d *AlertDispatcher) maxAttempts() int {
	if d.MaxAttempts > 0 {
		return d.MaxAttempts
	}
	return defaultAlertMaxAttempts
}

func (d *AlertDispatcher) backoffForAttempt(attempt int) time.Duration {
	steps := d.Backoffs
	if len(steps) == 0 {
		steps = defaultRetryBackoffs
	}
	if attempt <= 1 {
		return steps[0]
	}
	idx := attempt - 1
	if idx >= len(steps) {
		idx = len(steps) - 1
	}
	return steps[idx]
}

// matchesTransitionChannel 严格执行 Default Deny 订阅判定：
// - alert 要求渠道显式订阅该 rule_key（或 "*"）；
// - recovery 要求渠道显式订阅 "recovery" 或对应 rule_key（或 "*"）；
// - 未开启、空配置或非法 JSON 渠道一律返回 false（零广播）。
func matchesTransitionChannel(subJSON string, instanceID uint, tr EventTransition) bool {
	if tr.Kind == DeliveryKindRecovery {
		return notify.MatchesSiteOpsChannel(subJSON, instanceID, DeliveryKindRecovery) ||
			notify.MatchesSiteOpsChannel(subJSON, instanceID, tr.Event.RuleKey)
	}
	return notify.MatchesSiteOpsChannel(subJSON, instanceID, tr.Event.RuleKey)
}

// FormatTransitionMessage 生成脱敏、零 PII 的告警/恢复通知标题与正文。
func FormatTransitionMessage(inst storage.SiteOpsInstance, tr EventTransition) (subject, body string) {
	siteName := SanitizeErrorText(inst.Name, 80)
	if siteName == "" {
		siteName = fmt.Sprintf("instance#%d", inst.ID)
	}
	title := SanitizeErrorText(tr.Event.Title, 160)
	if title == "" {
		title = tr.Event.RuleKey
	}
	summary := SanitizeErrorText(tr.Event.Summary, 400)
	resource := SanitizeErrorText(tr.Event.ResourceKey, 80)
	if resource == "" {
		resource = "site"
	}

	if tr.Kind == DeliveryKindRecovery {
		subject = fmt.Sprintf("[站点恢复] %s · %s", siteName, title)
	} else {
		sev := strings.ToUpper(strings.TrimSpace(tr.Event.Severity))
		if sev == "" {
			sev = "WARNING"
		}
		subject = fmt.Sprintf("[站点告警/%s] %s · %s", sev, siteName, title)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "站点：%s (ID=%d)\n", siteName, inst.ID)
	fmt.Fprintf(&b, "规则：%s\n", tr.Event.RuleKey)
	fmt.Fprintf(&b, "资源：%s\n", resource)
	if tr.Kind == DeliveryKindRecovery {
		b.WriteString("状态：已恢复\n")
		if tr.Event.ResolvedAt != nil {
			fmt.Fprintf(&b, "恢复时间：%s\n", tr.Event.ResolvedAt.In(locUTC8).Format("2006-01-02 15:04:05 UTC+8"))
		}
	} else {
		fmt.Fprintf(&b, "级别：%s\n", tr.Event.Severity)
		fmt.Fprintf(&b, "状态：告警中（连续命中 %d 次）\n", tr.Event.ConsecutiveHits)
		if !tr.Event.FiredAt.IsZero() {
			fmt.Fprintf(&b, "触发时间：%s\n", tr.Event.FiredAt.In(locUTC8).Format("2006-01-02 15:04:05 UTC+8"))
		}
	}
	if summary != "" {
		fmt.Fprintf(&b, "摘要：%s\n", summary)
	}
	return subject, strings.TrimSpace(b.String())
}

// DispatchTransitions 将状态机产出的新状态跃迁派发至显式订阅渠道。
func (d *AlertDispatcher) DispatchTransitions(
	ctx context.Context,
	inst storage.SiteOpsInstance,
	cfg AlertConfig,
	transitions []EventTransition,
) ([]storage.SiteOpsAlertDelivery, error) {
	if len(transitions) == 0 || d.store == nil || d.notifier == nil {
		return nil, nil
	}
	hasNew := false
	for _, tr := range transitions {
		if tr.NewlyTransitioned {
			hasNew = true
			break
		}
	}
	if !hasNew {
		return nil, nil
	}

	channels, err := d.notifier.ListEnabledChannels()
	if err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, nil
	}

	now := d.now()
	var results []storage.SiteOpsAlertDelivery

	for _, tr := range transitions {
		if !tr.NewlyTransitioned {
			continue
		}
		subject, body := FormatTransitionMessage(inst, tr)
		cooldownKey := fmt.Sprintf("%s:%s", tr.Event.RuleKey, tr.Event.ResourceKey)

		for _, ch := range channels {
			if !ch.Enabled {
				continue
			}
			if !matchesTransitionChannel(ch.SiteOpsSubscriptions, inst.ID, tr) {
				continue
			}

			if tr.Kind == DeliveryKindAlert {
				activeCooldown, err := d.store.HasActiveCooldown(ctx, inst.ID, ch.ID, cooldownKey, now)
				if err != nil {
					return results, err
				}
				if activeCooldown {
					skipped := storage.SiteOpsAlertDelivery{
						EventID:         tr.Event.ID,
						InstanceID:      inst.ID,
						NotifyChannelID: ch.ID,
						DeliveryKind:    DeliveryKindAlert,
						CooldownKey:     cooldownKey,
						Status:          DeliveryStatusSkippedCooldown,
						Attempts:        0,
						Subject:         subject,
						Body:            body,
						CreatedAt:       now,
						UpdatedAt:       now,
					}
					if err := d.store.CreateDelivery(ctx, &skipped); err != nil {
						return results, err
					}
					results = append(results, skipped)
					continue
				}

				var cooldownUntil *time.Time
				if cfg.CooldownMinutes > 0 {
					cu := now.Add(time.Duration(cfg.CooldownMinutes) * time.Minute)
					cooldownUntil = &cu
				}
				nextAt := now
				rec := storage.SiteOpsAlertDelivery{
					EventID:         tr.Event.ID,
					InstanceID:      inst.ID,
					NotifyChannelID: ch.ID,
					DeliveryKind:    DeliveryKindAlert,
					CooldownKey:     cooldownKey,
					CooldownUntil:   cooldownUntil,
					Status:          DeliveryStatusPending,
					Attempts:        0,
					NextRetryAt:     &nextAt,
					Subject:         subject,
					Body:            body,
					CreatedAt:       now,
					UpdatedAt:       now,
				}
				if err := d.store.CreateDelivery(ctx, &rec); err != nil {
					return results, err
				}
				if err := d.attemptDelivery(ctx, &rec, now); err != nil {
					return results, err
				}
				results = append(results, rec)
				continue
			}

			// 恢复通知不受告警冷却阻塞，直接按独立投递记录发送与退避重试。
			nextAt := now
			rec := storage.SiteOpsAlertDelivery{
				EventID:         tr.Event.ID,
				InstanceID:      inst.ID,
				NotifyChannelID: ch.ID,
				DeliveryKind:    DeliveryKindRecovery,
				CooldownKey:     cooldownKey,
				Status:          DeliveryStatusPending,
				Attempts:        0,
				NextRetryAt:     &nextAt,
				Subject:         subject,
				Body:            body,
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			if err := d.store.CreateDelivery(ctx, &rec); err != nil {
				return results, err
			}
			if err := d.attemptDelivery(ctx, &rec, now); err != nil {
				return results, err
			}
			results = append(results, rec)
		}
	}

	return results, nil
}

// ProcessDueDeliveries 扫描并补发到期的 pending/failed 投递记录，绝不触碰已 sent 的渠道。
func (d *AlertDispatcher) ProcessDueDeliveries(ctx context.Context, limit int) (int, error) {
	if d.store == nil || d.notifier == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = defaultDueDeliveryLimit
	}
	now := d.now()
	due, err := d.store.ListDueDeliveries(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	maxAtt := d.maxAttempts()
	for i := range due {
		rec := due[i]
		if rec.Status == DeliveryStatusSent || rec.Status == DeliveryStatusSkippedCooldown {
			continue
		}
		if rec.Attempts >= maxAtt {
			rec.NextRetryAt = nil
			rec.UpdatedAt = now
			if err := d.store.UpdateDelivery(ctx, &rec); err != nil {
				return processed, err
			}
			continue
		}
		if err := d.attemptDelivery(ctx, &rec, now); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

// DispatchDailyReport 向显式订阅 daily_report 的渠道直投 24h 运维日报并落库投递状态。
func (d *AlertDispatcher) DispatchDailyReport(
	ctx context.Context,
	inst storage.SiteOpsInstance,
	preview DailyReportPreview,
) (int, []storage.SiteOpsAlertDelivery, error) {
	if d.store == nil || d.notifier == nil {
		return 0, nil, nil
	}
	channels, err := d.notifier.ListEnabledChannels()
	if err != nil {
		return 0, nil, err
	}
	now := d.now()
	matched := 0
	var deliveries []storage.SiteOpsAlertDelivery

	for _, ch := range channels {
		if !ch.Enabled {
			continue
		}
		if !notify.MatchesSiteOpsChannel(ch.SiteOpsSubscriptions, inst.ID, DeliveryKindDailyReport) {
			continue
		}
		matched++
		nextAt := now
		rec := storage.SiteOpsAlertDelivery{
			EventID:         0,
			InstanceID:      inst.ID,
			NotifyChannelID: ch.ID,
			DeliveryKind:    DeliveryKindDailyReport,
			CooldownKey:     "daily_report:" + preview.DateUTC8,
			Status:          DeliveryStatusPending,
			Attempts:        0,
			NextRetryAt:     &nextAt,
			Subject:         preview.Subject,
			Body:            preview.Body,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := d.store.CreateDelivery(ctx, &rec); err != nil {
			return matched, deliveries, err
		}
		if err := d.attemptDelivery(ctx, &rec, now); err != nil {
			return matched, deliveries, err
		}
		deliveries = append(deliveries, rec)
	}
	return matched, deliveries, nil
}

func (d *AlertDispatcher) attemptDelivery(ctx context.Context, rec *storage.SiteOpsAlertDelivery, now time.Time) error {
	sendErr := d.notifier.SendDirect(ctx, rec.NotifyChannelID, rec.Subject, rec.Body)
	rec.Attempts++
	rec.UpdatedAt = now
	if sendErr == nil {
		rec.Status = DeliveryStatusSent
		sentAt := now
		rec.SentAt = &sentAt
		rec.NextRetryAt = nil
		rec.LastError = ""
	} else {
		rec.Status = DeliveryStatusFailed
		rec.LastError = SanitizeErrorText(sendErr.Error(), 240)
		if rec.Attempts < d.maxAttempts() {
			next := now.Add(d.backoffForAttempt(rec.Attempts))
			rec.NextRetryAt = &next
		} else {
			rec.NextRetryAt = nil
		}
	}
	return d.store.UpdateDelivery(ctx, rec)
}
