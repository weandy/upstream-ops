package intelligence

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bejix/upstream-ops/backend/notify"
	"github.com/bejix/upstream-ops/backend/storage"
)

// AlertDispatcher 通知分发抽象（生产实现为 *notify.Dispatcher）。
type AlertDispatcher interface {
	Dispatch(ctx context.Context, msg notify.Message) error
	Policy() notify.Policy
}

// CooldownStore 可选的持久化冷却仓储（生产实现为 *storage.Notifications，复用 notification_cooldowns 表）。
type CooldownStore interface {
	TryClaimCooldown(channelID uint, event storage.NotificationEvent, cooldown time.Duration) (bool, error)
	ResetCooldown(channelID uint, event storage.NotificationEvent) error
}

var beijingLocation = time.FixedZone("UTC+8", 8*3600)

func formatAlertTimeUTC8(t time.Time) string {
	return t.In(beijingLocation).Format("2006-01-02 15:04:05 (UTC+8)")
}

// Alerter 负责主动测活与智力检测的连续异常阈值状态机、冷却去抖与告警推送。
// 第一期严格边界：仅记录历史 + 推送通知，不自动禁用或修改上游渠道。
type Alerter struct {
	liveness   *storage.ProbeLivenessSamples
	runs       *storage.ProbeIntelligenceRuns
	dispatcher AlertDispatcher
	store      CooldownStore
	log        *slog.Logger

	mu       sync.Mutex
	downSet  map[uint]bool
	cooldown map[string]time.Time
}

func NewAlerter(
	liveness *storage.ProbeLivenessSamples,
	runs *storage.ProbeIntelligenceRuns,
	dispatcher AlertDispatcher,
	log *slog.Logger,
) *Alerter {
	return &Alerter{
		liveness:   liveness,
		runs:       runs,
		dispatcher: dispatcher,
		log:        log,
		downSet:    make(map[uint]bool),
		cooldown:   make(map[string]time.Time),
	}
}

// SetCooldownStore 注入持久化冷却仓储，确保进程重启后不丢失告警冷却窗口。
func (a *Alerter) SetCooldownStore(store CooldownStore) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.store = store
	a.mu.Unlock()
}

// HandleResult 作为 Service.SetResultHook 的回调函数，在每次检测结果落库后评估告警。
func (a *Alerter) HandleResult(
	ctx context.Context,
	target *storage.ProbeTarget,
	cfg *storage.ProbeDetectorConfig,
	res *DetectorRunResult,
) {
	if a == nil || a.dispatcher == nil || target == nil || cfg == nil || res == nil {
		return
	}
	if !cfg.AlertEnabled {
		return
	}
	threshold := cfg.AlertThreshold
	if threshold <= 0 {
		threshold = 3
	}

	if cfg.DetectorKey == DetectorKeyLiveness {
		a.handleLivenessAlert(ctx, target, cfg, res, threshold)
		return
	}
	a.handleIntelligenceAlert(ctx, target, cfg, res, threshold)
}

func (a *Alerter) handleLivenessAlert(
	ctx context.Context,
	target *storage.ProbeTarget,
	cfg *storage.ProbeDetectorConfig,
	res *DetectorRunResult,
	threshold int,
) {
	if res.LivenessSample != nil && res.LivenessSample.Success {
		a.mu.Lock()
		wasDown := a.downSet[target.ID]
		if wasDown {
			delete(a.downSet, target.ID)
		}
		a.mu.Unlock()
		// 进程重启后内存 downSet 为空，通过检查本次成功前紧邻的 threshold 条历史样本推导是否刚从掉线恢复。
		if !wasDown && a.liveness != nil {
			recent, err := a.liveness.ListRecentByTarget(target.ID, threshold+1)
			if err == nil && len(recent) >= threshold+1 && recent[0].Success {
				prevAllFailed := true
				for _, s := range recent[1 : threshold+1] {
					if s.Success {
						prevAllFailed = false
						break
					}
				}
				wasDown = prevAllFailed
			}
		}
		if !wasDown {
			return
		}
		a.resetCooldown(target.ID, DetectorKeyLiveness, storage.EventProbeDown)
		msg := notify.Message{
			Event:     storage.EventProbeRecovered,
			ChannelID: target.ChannelID,
			ModelName: cfg.ModelName,
			Subject:   fmt.Sprintf("【测活恢复】%s（%s）", target.Name, cfg.ModelName),
			Body: fmt.Sprintf(
				"# 主动测活已恢复\n\n- **目标**：%s\n- **模型**：`%s`\n- **首字延迟**：`%d ms`\n- **生成速度**：`%.1f t/s`\n- **时间**：%s\n",
				target.Name, cfg.ModelName, res.FirstTokenMS, res.LivenessSample.TPS, formatAlertTimeUTC8(time.Now()),
			),
			Extra: map[string]any{"target_id": target.ID, "target_name": target.Name, "detector": DetectorKeyLiveness},
		}
		a.dispatch(ctx, msg)
		return
	}

	recent, err := a.liveness.ListRecentByTarget(target.ID, threshold)
	if err != nil || len(recent) < threshold {
		return
	}
	for _, s := range recent {
		if s.Success {
			return
		}
	}

	a.mu.Lock()
	a.downSet[target.ID] = true
	a.mu.Unlock()

	if !a.claimCooldown(target.ID, DetectorKeyLiveness, storage.EventProbeDown) {
		return
	}
	errReason := strings.TrimSpace(res.ErrorMessage)
	if errReason == "" && res.LivenessSample != nil {
		errReason = res.LivenessSample.ErrorClass
	}
	msg := notify.Message{
		Event:     storage.EventProbeDown,
		ChannelID: target.ChannelID,
		ModelName: cfg.ModelName,
		Subject:   fmt.Sprintf("【测活异常】%s 连续 %d 次失败", target.Name, threshold),
		Body: fmt.Sprintf(
			"# 主动测活连续失败\n\n- **目标**：%s\n- **模型**：`%s`\n- **连续失败次数**：%d\n- **最近原因**：%s\n- **时间**：%s\n",
			target.Name, cfg.ModelName, threshold, errReason, formatAlertTimeUTC8(time.Now()),
		),
		Extra: map[string]any{"target_id": target.ID, "target_name": target.Name, "detector": DetectorKeyLiveness},
	}
	a.dispatch(ctx, msg)
}

func (a *Alerter) handleIntelligenceAlert(
	ctx context.Context,
	target *storage.ProbeTarget,
	cfg *storage.ProbeDetectorConfig,
	res *DetectorRunResult,
	threshold int,
) {
	var event storage.NotificationEvent
	var targetStatus string
	var titlePrefix string
	switch res.Status {
	case storage.IntelligenceStatusSuspected:
		event = storage.EventIntelligenceSuspected
		targetStatus = storage.IntelligenceStatusSuspected
		titlePrefix = "疑似降智/模型偏移"
	case storage.IntelligenceStatusError:
		event = storage.EventIntelligenceError
		targetStatus = storage.IntelligenceStatusError
		titlePrefix = "智力检测连续报错"
	default:
		// 恢复正常时清掉对应事件的冷却（含内存与持久化），方便下次再出异常时及时通知。
		a.resetCooldown(target.ID, cfg.DetectorKey, storage.EventIntelligenceSuspected)
		a.resetCooldown(target.ID, cfg.DetectorKey, storage.EventIntelligenceError)
		return
	}

	recent, err := a.runs.ListByTargetAndDetector(target.ID, cfg.DetectorKey, threshold)
	if err != nil || len(recent) < threshold {
		return
	}
	for _, r := range recent {
		if r.Status != targetStatus {
			return
		}
	}

	if !a.claimCooldown(target.ID, cfg.DetectorKey, event) {
		return
	}
	detail := strings.TrimSpace(res.Summary)
	if res.ErrorMessage != "" {
		detail = detail + " (" + res.ErrorMessage + ")"
	}
	msg := notify.Message{
		Event:     event,
		ChannelID: target.ChannelID,
		ModelName: cfg.ModelName,
		Subject:   fmt.Sprintf("【%s】%s · %s（连续 %d 次）", titlePrefix, target.Name, cfg.DetectorKey, threshold),
		Body: fmt.Sprintf(
			"# %s\n\n- **目标**：%s\n- **检测插件**：`%s`\n- **检测模型**：`%s`\n- **连续异常次数**：%d\n- **最近摘要**：%s\n- **时间**：%s\n",
			titlePrefix, target.Name, cfg.DetectorKey, cfg.ModelName, threshold, detail, formatAlertTimeUTC8(time.Now()),
		),
		Extra: map[string]any{"target_id": target.ID, "target_name": target.Name, "detector": cfg.DetectorKey},
	}
	a.dispatch(ctx, msg)
}

func (a *Alerter) claimCooldown(targetID uint, detectorKey string, event storage.NotificationEvent) bool {
	cd := a.dispatcher.Policy().IntelligenceAlertCooldown
	if cd <= 0 {
		return true
	}
	a.mu.Lock()
	store := a.store
	a.mu.Unlock()
	if store != nil {
		ok, err := store.TryClaimCooldown(targetID, persistedCooldownEvent(detectorKey, event), cd)
		if err == nil {
			return ok
		}
		if a.log != nil {
			a.log.Warn("persistent intelligence cooldown failed, falling back to memory", "target_id", targetID, "event", event, "err", err)
		}
	}
	key := cooldownKey(targetID, detectorKey, event)
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if last, ok := a.cooldown[key]; ok && now.Sub(last) < cd {
		return false
	}
	a.cooldown[key] = now
	return true
}

func (a *Alerter) resetCooldown(targetID uint, detectorKey string, event storage.NotificationEvent) {
	a.mu.Lock()
	delete(a.cooldown, cooldownKey(targetID, detectorKey, event))
	store := a.store
	a.mu.Unlock()
	if store != nil {
		if err := store.ResetCooldown(targetID, persistedCooldownEvent(detectorKey, event)); err != nil && a.log != nil {
			a.log.Warn("reset persistent intelligence cooldown failed", "target_id", targetID, "event", event, "err", err)
		}
	}
}

func persistedCooldownEvent(detectorKey string, event storage.NotificationEvent) storage.NotificationEvent {
	// 复用 notification_cooldowns(channel_id, event) 唯一键；带 intel: 前缀与普通渠道告警命名空间隔离，长度 <= 64。
	raw := "intel:" + string(event) + ":" + strings.TrimSpace(detectorKey)
	if len(raw) > 64 {
		raw = raw[:64]
	}
	return storage.NotificationEvent(raw)
}

func (a *Alerter) dispatch(ctx context.Context, msg notify.Message) {
	if err := a.dispatcher.Dispatch(ctx, msg); err != nil && a.log != nil {
		a.log.Warn("dispatch intelligence alert failed", "event", msg.Event, "subject", msg.Subject, "err", err)
	}
}

func cooldownKey(targetID uint, detectorKey string, event storage.NotificationEvent) string {
	return fmt.Sprintf("%d:%s:%s", targetID, detectorKey, event)
}
