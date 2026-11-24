package embed

import (
	"fmt"
	"html/template"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/intelligence"
)

var embedFuncMap = template.FuncMap{
	"queryEscape": url.QueryEscape,
	"fmtFloat1": func(v float64) string {
		if v <= 0 {
			return "--"
		}
		return fmt.Sprintf("%.1f", v)
	},
	"fmtPct": func(v float64, samples int) string {
		if samples <= 0 {
			return "--"
		}
		return fmt.Sprintf("%.1f%%", v)
	},
	"fmtMS": func(ms int64) string {
		if ms <= 0 {
			return "--"
		}
		if ms >= 1000 {
			return fmt.Sprintf("%.2fs", float64(ms)/1000.0)
		}
		return fmt.Sprintf("%dms", ms)
	},
	"fmtProb": func(p float64) string {
		if p < 0 {
			p = 0
		}
		pct := p * 100
		if pct > 100 {
			pct = 100
		}
		return fmt.Sprintf("%.1f%%", pct)
	},
	"probPct": func(p float64) int {
		if p <= 0 {
			return 0
		}
		v := int(math.Round(p * 100))
		if v > 100 {
			return 100
		}
		return v
	},
	"probWidthStyle": func(p float64) template.CSS {
		v := int(math.Round(p * 100))
		if v < 0 {
			v = 0
		}
		if v > 100 {
			v = 100
		}
		return template.CSS(fmt.Sprintf("width:%d%%", v))
	},
	"fmtTimeShort": func(t time.Time) string {
		if t.IsZero() {
			return "--"
		}
		return t.Local().Format("01-02 15:04")
	},
	"fmtClock": func(t time.Time) string {
		if t.IsZero() {
			return "--:--"
		}
		return t.Local().Format("15:04")
	},
	"fmtTimeFull": func(t time.Time) string {
		if t.IsZero() {
			return "--"
		}
		return t.Local().Format("2006-01-02 15:04:05")
	},
	"fmtTimePtr": func(t *time.Time) string {
		if t == nil || t.IsZero() {
			return "暂无采样"
		}
		return t.Local().Format("01-02 15:04:05")
	},
	"fmtRelative": func(now time.Time, t *time.Time) string {
		if t == nil || t.IsZero() {
			return "等待首轮探测"
		}
		d := now.Sub(*t)
		if d < 0 {
			d = 0
		}
		switch {
		case d < time.Minute:
			return fmt.Sprintf("%d 秒前", int(d.Seconds()))
		case d < time.Hour:
			return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
		case d < 24*time.Hour:
			return fmt.Sprintf("%d 小时前", int(d.Hours()))
		default:
			return fmt.Sprintf("%d 天前", int(d.Hours()/24))
		}
	},
	"familyLabel": func(f string) string {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case "claude":
			return "Claude"
		case "codex":
			return "Codex"
		case "grok":
			return "Grok"
		case "domestic":
			return "国产"
		default:
			return "通用"
		}
	},
	"familyClass": func(f string) string {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case "claude", "codex", "grok", "domestic":
			return "fam-" + strings.ToLower(strings.TrimSpace(f))
		default:
			return "fam-other"
		}
	},
	"statusLabel": func(s string) string {
		switch s {
		case "normal":
			return "正常"
		case "suspected":
			return "疑似异常"
		case "error":
			return "探测出错"
		default:
			return "待测"
		}
	},
	"statusClass": func(s string) string {
		switch s {
		case "normal":
			return "st-ok"
		case "suspected":
			return "st-warn"
		case "error":
			return "st-down"
		default:
			return "st-muted"
		}
	},
	"rateClass": func(rate float64, samples int) string {
		if samples <= 0 {
			return "st-muted"
		}
		switch {
		case rate >= 95:
			return "st-ok"
		case rate >= 80:
			return "st-warn"
		default:
			return "st-down"
		}
	},
	"trendLabel": func(minutes int) string {
		if minutes <= 0 {
			minutes = intelligence.TrendBucketCount
		}
		cadence := fmt.Sprintf("每 %d 秒", minutes)
		if minutes%60 == 0 {
			cadence = fmt.Sprintf("每 %d 分钟", minutes/60)
		}
		return fmt.Sprintf("近 %d 分钟可用率 · %s测试一次", minutes, cadence)
	},
	"bucketClock": func(ts time.Time, windowMinutes int) string {
		if ts.IsZero() {
			return "--:--"
		}
		if windowMinutes <= 0 {
			windowMinutes = intelligence.TrendBucketCount
		}
		start := ts.Local()
		bucket := time.Duration(windowMinutes) * time.Minute / time.Duration(intelligence.TrendBucketCount)
		if bucket <= time.Minute {
			return start.Format("15:04")
		}
		return start.Format("15:04") + "–" + start.Add(bucket).Format("15:04")
	},
	"bucketDetail": func(samples, okCount int, rate float64) string {
		if samples <= 0 {
			return "无采样"
		}
		return fmt.Sprintf("可用率 %.0f%% · %d/%d", rate, okCount, samples)
	},
	"themeClass": func(theme string) string {
		return "theme-" + normalizeTheme(theme)
	},
}

const sharedCSS = `
:root {
  color-scheme: light;
  --bg: #f7f5ef;
  --surface: #fffefa;
  --surface-alt: #f2efe7;
  --surface-hover: #faf7f0;
  --border: #e7e2d8;
  --border-strong: #d5cec0;
  --ink: #25231f;
  --ink-muted: #706c64;
  --ink-subtle: #948f85;
  --accent: #d8593a;
  --ok: #0eaf78;
  --ok-bg: #e3f6ee;
  --warn: #d89a20;
  --warn-bg: #fcf2dc;
  --down: #d94a42;
  --down-bg: #fce7e5;
  --none: #cfc8bc;
  --shadow: 0 12px 35px rgba(45, 38, 26, 0.05);
  --radius: 18px;
}
body.theme-light {
  color-scheme: light;
  --bg: #f4f7fb;
  --surface: #ffffff;
  --surface-alt: #f1f5f9;
  --surface-hover: #f8fafc;
  --border: #e2e8f0;
  --border-strong: #cbd5e1;
  --ink: #0f172a;
  --ink-muted: #475569;
  --ink-subtle: #64748b;
  --accent: #0f766e;
  --accent-soft: #ccfbf1;
  --ok: #10b981;
  --ok-bg: #dcfce7;
  --warn: #d97706;
  --warn-bg: #fef3c7;
  --down: #e11d48;
  --down-bg: #fee2e2;
  --none: #cbd5e1;
  --shadow: 0 10px 28px rgba(15, 23, 42, 0.045);
}
body.theme-dark {
  color-scheme: dark;
  --bg: #0c1016;
  --surface: #131922;
  --surface-alt: #1a222e;
  --surface-hover: #1c2533;
  --border: #253041;
  --border-strong: #36455c;
  --ink: #e9eff6;
  --ink-muted: #98a6b8;
  --ink-subtle: #6f7f94;
  --accent: #3fb984;
  --ok: #22c55e;
  --ok-bg: rgba(34, 197, 94, 0.14);
  --warn: #eab308;
  --warn-bg: rgba(234, 179, 8, 0.16);
  --down: #f43f5e;
  --down-bg: rgba(244, 63, 94, 0.16);
  --none: #2b3648;
  --shadow: 0 12px 32px rgba(0, 0, 0, 0.35);
}
* { box-sizing: border-box; }
html, body {
  margin: 0;
  padding: 0;
  background: var(--bg);
  color: var(--ink);
  font-family: "PingFang SC", "Microsoft YaHei", -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
  font-size: 13px;
  line-height: 1.5;
  -webkit-font-smoothing: antialiased;
}
a { color: inherit; text-decoration: none; }
.wrap {
  max-width: 1220px;
  margin: 0 auto;
  padding: 12px;
}
.shell {
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow);
  overflow: hidden;
}
.hdr {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  padding: 20px 22px 14px;
}
.eyebrow {
  margin: 0 0 4px;
  font-size: 11px;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--accent);
  font-weight: 700;
}
.title {
  margin: 0;
  font-family: "Songti SC", "Noto Serif SC", "STSong", Georgia, serif;
  font-size: 22px;
  font-weight: 600;
  letter-spacing: 0.01em;
  line-height: 1.25;
}
body.theme-light .title,
body.theme-dark .title {
  font-family: "PingFang SC", "Microsoft YaHei", system-ui, sans-serif;
}
.meta-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  color: var(--ink-muted);
  font-size: 11px;
}
.pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 9px;
  border-radius: 999px;
  background: var(--surface-alt);
  border: 1px solid var(--border);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--ok);
  display: inline-block;
}

.table-wrap {
  width: 100%;
  overflow: visible;
}
table.perf-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}
table.perf-table .col-channel  { width: 12%; }
table.perf-table .col-tps      { width: 9%; }
table.perf-table .col-first    { width: 10%; }
table.perf-table .col-latency  { width: 9%; }
table.perf-table .col-success  { width: 9%; }
table.perf-table .col-identity { width: 17%; }
table.perf-table .col-trend    { width: 34%; }
table.perf-table.no-identity .col-channel { width: 15%; }
table.perf-table.no-identity .col-tps     { width: 10%; }
table.perf-table.no-identity .col-first   { width: 12%; }
table.perf-table.no-identity .col-latency { width: 11%; }
table.perf-table.no-identity .col-success { width: 10%; }
table.perf-table.no-identity .col-trend   { width: 42%; }

table.perf-table th,
table.perf-table td {
  padding: 11px 10px;
  border-top: 1px solid var(--border);
  text-align: center;
  vertical-align: middle;
}
table.perf-table th:first-child,
table.perf-table td:first-child {
  padding-left: 20px;
  text-align: left;
}
table.perf-table th:last-child,
table.perf-table td:last-child {
  padding-right: 20px;
}
table.perf-table thead th {
  color: var(--ink-muted);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.02em;
  white-space: nowrap;
}
table.perf-table tbody td {
  font-size: 12px;
}
table.perf-table tbody tr:hover {
  background: var(--surface-hover);
}

.num {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.unit {
  font-weight: 500;
  color: var(--ink-muted);
  margin-left: 2px;
  font-size: 11px;
}

/* 渠道名胶囊（复刻圆点+家族柔和色） */
.channel-pill {
  display: inline-flex;
  max-width: 100%;
  align-items: center;
  gap: 6px;
  padding: 3px 8px;
  border-radius: 6px;
  font-weight: 700;
  font-size: 11.5px;
  white-space: nowrap;
  background: var(--fam-bg, #eef0f2);
  color: var(--fam-fg, #59616c);
}
.channel-pill::before {
  content: "";
  width: 6px;
  height: 6px;
  flex: 0 0 auto;
  border-radius: 50%;
  background: currentColor;
}
.channel-name {
  overflow: hidden;
  text-overflow: ellipsis;
}
.fam-codex    { --fam-fg: #087c89; --fam-bg: #e7f4f5; }
.fam-claude   { --fam-fg: #ad542f; --fam-bg: #f8ece6; }
.fam-grok     { --fam-fg: #46505e; --fam-bg: #edf0f3; }
.fam-domestic { --fam-fg: #7252ad; --fam-bg: #f1ecf8; }
.fam-other    { --fam-fg: #59616c; --fam-bg: #eef0f2; }
body.theme-dark .fam-codex    { --fam-fg: #5bd6e0; --fam-bg: rgba(14, 124, 137, 0.22); }
body.theme-dark .fam-claude   { --fam-fg: #f3a177; --fam-bg: rgba(173, 84, 47, 0.22); }
body.theme-dark .fam-grok     { --fam-fg: #9fb3c8; --fam-bg: rgba(70, 80, 94, 0.28); }
body.theme-dark .fam-domestic { --fam-fg: #c4a7f2; --fam-bg: rgba(114, 82, 173, 0.24); }
body.theme-dark .fam-other    { --fam-fg: #9aa7b8; --fam-bg: rgba(89, 97, 108, 0.24); }

.fam-tag {
  display: inline-flex;
  align-items: center;
  padding: 2px 7px;
  border-radius: 5px;
  font-size: 10px;
  font-weight: 700;
  background: var(--fam-bg, #eef0f2);
  color: var(--fam-fg, #59616c);
}

.st-ok    { color: var(--ok); }
.st-warn  { color: var(--warn); }
.st-down  { color: var(--down); }
.st-muted { color: var(--ink-subtle); }

/* 模型检测列：左侧 Pelican 缩略图 + 右侧紧凑圆点状态行 */
td.identity-cell {
  padding-top: 7px;
  padding-bottom: 7px;
}
.checks {
  display: flex;
  width: 230px;
  max-width: 100%;
  min-width: 0;
  margin: 0 auto;
  align-items: center;
  gap: 8px;
  text-align: left;
}
.check-lines {
  display: flex;
  min-width: 0;
  flex: 1 1 auto;
  flex-direction: column;
  gap: 2.5px;
}
.check {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 5px;
  color: var(--ink);
  font-size: 10.5px;
  line-height: 1.35;
  white-space: nowrap;
}
.check::before {
  content: "";
  width: 6px;
  height: 6px;
  flex: 0 0 auto;
  border-radius: 50%;
  background: var(--tone, var(--ink-subtle));
}
.check.st-ok    { --tone: var(--ok); }
.check.st-warn  { --tone: var(--warn); }
.check.st-down  { --tone: var(--down); }
.check.st-muted { --tone: var(--none); }
a.check:hover .check-text {
  text-decoration: underline;
  text-decoration-color: var(--border-strong);
  text-underline-offset: 2px;
}
.check-text {
  overflow: hidden;
  text-overflow: ellipsis;
}
.check-key   { color: var(--ink-muted); }
.check-state { color: var(--tone); font-weight: 650; }
.check-count { color: var(--ink-muted); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.check-value { color: var(--tone); font-weight: 650; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }

.pelican-thumb {
  position: relative;
  display: block;
  flex: 0 0 auto;
  width: 40px;
  height: 30px;
  overflow: hidden;
  border: 1px solid var(--border-strong);
  border-radius: 5px;
  background: #f4f7f4;
  transition: border-color 0.15s ease, transform 0.15s ease;
}
.pelican-thumb:hover {
  border-color: var(--accent);
  transform: scale(1.05);
}
.pelican-thumb iframe {
  position: absolute;
  top: 0;
  left: 0;
  width: 400px;
  height: 300px;
  border: 0;
  transform: scale(0.1);
  transform-origin: 0 0;
  pointer-events: none;
}

/* 近 60 分钟可用率：格子用整数像素，不把余数摊进某一格。
   宽度按外层容器分档；悬停时当前这一分钟放大，方便对上时间。 */
.trend-slot {
  container-type: inline-size;
  width: 100%;
  min-width: 0;
  padding: 6px 0;
}
.trend-caption {
  width: max-content;
  max-width: 100%;
  margin: 0 0 2px;
  font-size: 10px;
  font-weight: 600;
  line-height: 1.3;
  color: var(--ink-muted);
  white-space: normal;
}
.trend-grid {
  display: flex;
  width: max-content;
  max-width: 100%;
  height: 22px;
  align-items: center;
  gap: 1px;
  overflow: visible;
}
.tcell {
  position: relative;
  z-index: 0;
  flex: 0 0 3px;
  width: 3px;
  height: 18px;
}
.tbar {
  display: block;
  width: 100%;
  height: 100%;
  border-radius: 1.5px;
  opacity: 0.9;
  background: var(--none);
  transform-origin: center center;
  transition: transform 0.12s ease, opacity 0.12s ease;
}
@container (min-width: 320px) {
  .tcell { flex-basis: 4px; width: 4px; }
}
@container (min-width: 420px) {
  .trend-grid { gap: 2px; }
  .tcell { flex-basis: 5px; width: 5px; }
}
.tcell:hover { z-index: 5; }
.tcell:hover .tbar {
  opacity: 1;
  transform: scaleX(2.4) scaleY(1.5);
}
.tip {
  position: absolute;
  z-index: 8;
  left: 50%;
  bottom: calc(100% + 8px);
  display: none;
  min-width: 96px;
  padding: 6px 9px 7px;
  border: 1px solid var(--border-strong);
  border-radius: 8px;
  background: var(--surface);
  color: var(--ink);
  box-shadow: 0 8px 18px rgba(37, 35, 31, 0.16);
  transform: translateX(-50%);
  pointer-events: none;
  white-space: nowrap;
}
.tip::after {
  content: "";
  position: absolute;
  left: 50%;
  top: 100%;
  width: 8px;
  height: 8px;
  margin-left: -4px;
  background: var(--surface);
  border-right: 1px solid var(--border-strong);
  border-bottom: 1px solid var(--border-strong);
  transform: translateY(-5px) rotate(45deg);
}
.tcell:hover .tip { display: block; }
.tcell:nth-child(-n+7) .tip { left: 0; transform: none; }
.tcell:nth-child(-n+7) .tip::after { left: 8px; margin-left: 0; }
.tcell:nth-last-child(-n+7) .tip { left: auto; right: 0; transform: none; }
.tcell:nth-last-child(-n+7) .tip::after { left: auto; right: 4px; margin-left: 0; }
.tip-time {
  display: block;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 13px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  line-height: 1.2;
}
.tip-detail {
  display: block;
  margin-top: 2px;
  font-size: 11px;
  line-height: 1.2;
  color: var(--ink-muted);
}
.tbar.ok   { background: #17803d; }
.tbar.warn { background: #b4690e; }
.tbar.down { background: #d93036; }
.tbar.none { background: var(--none); opacity: 0.55; }
.tcell:hover .tbar.none { opacity: 0.9; }
body.theme-dark .tbar.ok   { background: #22c55e; }
body.theme-dark .tbar.warn { background: #eab308; }
body.theme-dark .tbar.down { background: #f43f5e; }

.ftr {
  padding: 10px 22px 12px;
  border-top: 1px solid var(--border);
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 16px;
  font-size: 11px;
  color: var(--ink-muted);
}
.legend {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.legend-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.legend-swatch {
  width: 8px;
  height: 8px;
  border-radius: 2px;
  display: inline-block;
}
.empty {
  padding: 42px 18px;
  text-align: center;
  color: var(--ink-muted);
}

/* 钻取子页通用样式 */
.sub-panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow);
  overflow: hidden;
}
.back-link {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: var(--accent);
  font-weight: 700;
  margin-bottom: 10px;
}
.back-link:hover { text-decoration: underline; }
.notice {
  background: var(--surface);
  border: 1px solid var(--border);
  border-left: 3px solid var(--accent);
  border-radius: 10px;
  padding: 10px 14px;
  font-size: 12px;
  color: var(--ink-muted);
  margin: 0 0 12px;
}
.badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 7px;
  border-radius: 6px;
  font-size: 11px;
  font-weight: 600;
  white-space: nowrap;
}
.badge.st-ok    { background: var(--ok-bg); color: var(--ok); }
.badge.st-warn  { background: var(--warn-bg); color: var(--warn); }
.badge.st-down  { background: var(--down-bg); color: var(--down); }
.badge.st-muted { background: var(--surface-alt); color: var(--ink-muted); }
.intel-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.strip-48 {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border);
  background: var(--surface-hover);
}
.strip-cell {
  width: 14px;
  height: 24px;
  border-radius: 3px;
  display: inline-block;
  background: var(--none);
  transition: transform 0.12s ease;
}
.strip-cell:hover { transform: scaleY(1.14); }
.strip-cell.st-ok   { background: var(--ok); }
.strip-cell.st-warn { background: var(--warn); }
.strip-cell.st-down { background: var(--down); }
details.run-item {
  border-bottom: 1px solid var(--border);
}
details.run-item:last-child { border-bottom: none; }
details.run-item > summary {
  list-style: none;
  cursor: pointer;
  padding: 11px 18px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}
details.run-item > summary::-webkit-details-marker { display: none; }
details.run-item > summary:hover { background: var(--surface-hover); }
.run-body {
  padding: 12px 18px 16px;
  background: var(--surface-alt);
  border-top: 1px solid var(--border);
}
pre.answer-box {
  margin: 8px 0 0;
  padding: 11px 13px;
  border-radius: 8px;
  background: var(--surface);
  border: 1px solid var(--border);
  white-space: pre-wrap;
  word-break: break-word;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.55;
}
.cand-list {
  display: grid;
  gap: 8px;
  margin-top: 10px;
}
.cand-row {
  display: grid;
  grid-template-columns: 32px minmax(140px, 1.4fr) minmax(100px, 1fr) 54px;
  align-items: center;
  gap: 10px;
  font-size: 12px;
}
.prob-track {
  width: 100%;
  height: 8px;
  border-radius: 999px;
  background: var(--border);
  overflow: hidden;
}
.prob-fill {
  display: block;
  height: 100%;
  border-radius: 999px;
  background: var(--ok);
}
.gallery-stage {
  position: relative;
}
.col-radio {
  position: absolute;
  opacity: 0;
  pointer-events: none;
  width: 0;
  height: 0;
  margin: 0;
}
.gallery-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px 14px;
  flex-wrap: wrap;
  padding: 10px 18px;
  border-top: 1px solid var(--border);
  background: var(--surface-hover);
}
.day-nav {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.day-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 10px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--ink-muted);
  font-size: 11.5px;
  font-weight: 600;
  transition: border-color 0.14s ease, color 0.14s ease, background 0.14s ease;
}
.day-pill:hover {
  border-color: var(--accent);
  color: var(--ink);
}
.day-pill-count {
  padding: 0 5px;
  border-radius: 999px;
  background: var(--surface-alt);
  color: var(--ink-muted);
  font-size: 10.5px;
  line-height: 1.5;
}
.col-switch {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 2px 3px 2px 9px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--surface-alt);
  font-size: 11px;
  color: var(--ink-muted);
  margin-left: auto;
}
.col-switch-label {
  margin-right: 4px;
  font-weight: 600;
}
.col-switch label {
  cursor: pointer;
  padding: 2px 9px;
  border-radius: 999px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-weight: 650;
  color: var(--ink-muted);
  user-select: none;
  transition: background 0.14s ease, color 0.14s ease;
}
.col-switch label:hover {
  color: var(--ink);
}
#cols-2:checked ~ .gallery-toolbar label[for="cols-2"],
#cols-3:checked ~ .gallery-toolbar label[for="cols-3"],
#cols-4:checked ~ .gallery-toolbar label[for="cols-4"] {
  background: var(--surface);
  color: var(--accent);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08);
}
.day-groups {
  display: flex;
  flex-direction: column;
}
details.day-group {
  border-top: 1px solid var(--border);
  scroll-margin-top: 12px;
}
details.day-group > summary.day-header {
  list-style: none;
  cursor: pointer;
  padding: 13px 18px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
  background: var(--surface);
  transition: background 0.12s ease;
}
details.day-group > summary.day-header::-webkit-details-marker {
  display: none;
}
details.day-group > summary.day-header:hover {
  background: var(--surface-hover);
}
.day-heading {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.day-chevron {
  width: 0;
  height: 0;
  border-left: 5px solid var(--ink-muted);
  border-top: 4px solid transparent;
  border-bottom: 4px solid transparent;
  transition: transform 0.15s ease;
  flex-shrink: 0;
}
details.day-group[open] > summary.day-header .day-chevron {
  transform: rotate(90deg);
}
.day-label {
  font-size: 14px;
  font-weight: 700;
  color: var(--ink);
}
.day-sub {
  font-size: 11.5px;
  color: var(--ink-muted);
}
.gallery-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 14px;
  padding: 4px 18px 20px;
}
#cols-2:checked ~ .day-groups .gallery-grid {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
#cols-3:checked ~ .day-groups .gallery-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}
#cols-4:checked ~ .day-groups .gallery-grid {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}
.gallery-card {
  min-width: 0;
  background: var(--surface-alt);
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  transition: border-color 0.14s ease, box-shadow 0.14s ease;
}
.gallery-card:hover {
  border-color: var(--border-strong);
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.06);
}
.gallery-frame {
  width: 100%;
  aspect-ratio: 4 / 3;
  height: auto;
  border: 0;
  background: var(--surface-alt);
  display: block;
}
.gallery-meta {
  padding: 9px 12px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  font-size: 11px;
  color: var(--ink-muted);
  border-top: 1px solid var(--border);
  min-width: 0;
}
.gallery-meta-left {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.gallery-meta-right {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
.gallery-open {
  color: var(--accent);
  font-weight: 600;
  white-space: nowrap;
}
.gallery-open:hover {
  text-decoration: underline;
}

/* 中屏平板自适应 */
@media (max-width: 960px) {
  .hdr { padding: 16px 16px 12px; }
  table.perf-table th,
  table.perf-table td { padding: 9px 7px; }
  table.perf-table th:first-child,
  table.perf-table td:first-child { padding-left: 14px; }
  table.perf-table th:last-child,
  table.perf-table td:last-child { padding-right: 14px; }
  .unit { display: none; }
  .gallery-toolbar,
  details.day-group > summary.day-header {
    padding-left: 14px;
    padding-right: 14px;
  }
  .gallery-grid {
    gap: 12px;
    padding: 4px 14px 16px;
  }
  #cols-4:checked ~ .day-groups .gallery-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

/* 手机端 / 窄侧边栏 iframe：零横向滚动，自动切换为紧凑双列/三列卡片 */
@media (max-width: 720px) {
  .wrap { padding: 8px; }
  .hdr { padding: 14px 14px 10px; }
  .title { font-size: 18px; }
  .ftr { padding: 10px 14px; }
  .table-wrap { overflow-x: visible; }
  table.perf-table,
  table.perf-table tbody {
    display: block;
    width: 100%;
  }
  table.perf-table colgroup,
  table.perf-table thead { display: none; }
  table.perf-table tbody tr {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px;
    padding: 13px 14px;
    border-top: 1px solid var(--border);
  }
  table.perf-table tbody td,
  table.perf-table tbody td:first-child,
  table.perf-table tbody td:last-child {
    display: block;
    padding: 0;
    border: none;
    text-align: left;
    min-width: 0;
  }
  table.perf-table tbody td.cell-channel {
    grid-column: 1 / 3;
    align-self: center;
  }
  table.perf-table tbody td.cell-rate {
    grid-column: 3 / 4;
    text-align: right;
    align-self: center;
    font-size: 14px;
  }
  table.perf-table tbody td.cell-metric {
    background: var(--surface-alt);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 6px 8px;
  }
  table.perf-table tbody td.cell-metric::before {
    content: attr(data-label);
    display: block;
    font-size: 9.5px;
    font-weight: 600;
    color: var(--ink-muted);
    margin-bottom: 1px;
    font-family: "PingFang SC", "Microsoft YaHei", sans-serif;
  }
  table.perf-table tbody td.identity-cell {
    grid-column: 1 / -1;
    padding-top: 2px;
  }
  .checks {
    width: 100%;
    margin: 0;
  }
  .check-lines {
    flex-direction: row;
    flex-wrap: wrap;
    gap: 6px 12px;
  }
  table.perf-table tbody td.cell-trend {
    grid-column: 1 / -1;
    padding-top: 2px;
  }
  table.perf-table tbody td.cell-trend::before {
    content: attr(data-label);
    display: block;
    font-size: 9.5px;
    font-weight: 600;
    color: var(--ink-muted);
    margin-bottom: 4px;
  }
  .trend-caption { display: none; }
  .cand-row {
    grid-template-columns: 28px minmax(110px, 1fr) 76px 48px;
    gap: 6px;
  }
  #cols-3:checked ~ .day-groups .gallery-grid,
  #cols-4:checked ~ .day-groups .gallery-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 560px) {
  .col-switch { display: none; }
  .gallery-grid,
  #cols-2:checked ~ .day-groups .gallery-grid,
  #cols-3:checked ~ .day-groups .gallery-grid,
  #cols-4:checked ~ .day-groups .gallery-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
`

const tplOverview = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
{{- if gt .Site.RefreshSeconds 0 }}
<meta http-equiv="refresh" content="{{ .Site.RefreshSeconds }}">
{{- end }}
<title>{{ .Site.Title }}</title>
<style>` + sharedCSS + `</style>
</head>
<body class="{{ themeClass .Site.Theme }}">
<div class="wrap">
  <div class="shell">
    <header class="hdr">
      <div>
        <p class="eyebrow">{{ .Site.Eyebrow }}</p>
        <h1 class="title">{{ .Site.Title }}</h1>
      </div>
      <div class="meta-bar">
        <span class="pill"><span class="dot"></span>监控渠道 {{ len .Targets }}</span>
        <span class="pill">更新：{{ fmtRelative .GeneratedAt .LatestData }}</span>
      </div>
    </header>

    <main>
      {{- if eq (len .Targets) 0 }}
      <div class="empty">暂无可公开展示的监测渠道，请在管理台启用目标并勾选「对外 Embed 展示」。</div>
      {{- else }}
      <div class="table-wrap">
        <table class="perf-table {{ if .Site.ShowIdentityColumn }}has-identity{{ else }}no-identity{{ end }}">
          <colgroup>
            <col class="col-channel">
            <col class="col-tps">
            <col class="col-first">
            <col class="col-latency">
            <col class="col-success">
            {{- if .Site.ShowIdentityColumn }}
            <col class="col-identity">
            {{- end }}
            <col class="col-trend">
          </colgroup>
          <thead>
            <tr>
              <th>渠道</th>
              <th>TPS</th>
              <th>平均首 TOKEN 延迟</th>
              <th>平均延迟</th>
              <th>成功率 · 24h</th>
              {{- if .Site.ShowIdentityColumn }}
              <th>模型检测</th>
              {{- end }}
              <th>测活可用率</th>
            </tr>
          </thead>
          <tbody>
            {{- range .Targets }}
            <tr>
              <td class="cell-channel" data-label="渠道">
                <span class="channel-pill {{ familyClass .Family }}" title="{{ .Name }}">
                  <span class="channel-name">{{ .Name }}</span>
                </span>
              </td>
              <td class="num cell-metric" data-label="TPS">
                {{ fmtFloat1 .Performance.TPS }}{{- if gt .Performance.TPS 0.0 }}<span class="unit">t/s</span>{{- end }}
              </td>
              <td class="num cell-metric" data-label="平均首字">{{ fmtMS .Performance.AvgFirstTokenMS }}</td>
              <td class="num cell-metric" data-label="平均延迟">{{ fmtMS .Performance.AvgLatencyMS }}</td>
              <td class="num cell-rate {{ rateClass .Performance.SuccessRate .Performance.SampleCount }}" data-label="24h 成功率">
                {{ fmtPct .Performance.SuccessRate .Performance.SampleCount }}
              </td>
              {{- if $.Site.ShowIdentityColumn }}
              <td class="identity-cell" data-label="模型检测">
                <div class="checks">
                  {{- if and .Pelican .Pelican.LatestRunUUID }}
                  <a class="pelican-thumb" href="/embed/pelican?site={{ queryEscape $.Site.Slug }}&amp;target={{ queryEscape .Slug }}" title="查看最近的画鹈鹕 SVG 画廊">
                    <iframe sandbox="" loading="lazy" referrerpolicy="no-referrer" tabindex="-1" title="Pelican Preview" src="/embed/pelican/{{ queryEscape .Pelican.LatestRunUUID }}?site={{ queryEscape $.Site.Slug }}"></iframe>
                  </a>
                  {{- end }}
                  <div class="check-lines">
                    {{- if .Candy }}
                    <a class="check {{ statusClass .Candy.LatestStatus }}" href="/embed/candy?site={{ queryEscape $.Site.Slug }}&amp;target={{ queryEscape .Slug }}" title="查看最近 Candy 逻辑题抽检回答">
                      <span class="check-text">
                        <span class="check-key">Candy </span>
                        <span class="check-state">{{ statusLabel .Candy.LatestStatus }}</span>
                        {{- if gt .Candy.Checked24h 0 }} <span class="check-count">{{ .Candy.Passed24h }}/{{ .Candy.Checked24h }}</span>{{- end }}
                      </span>
                    </a>
                    {{- end }}
                    {{- if .Fingerprint }}
                    <a class="check {{ statusClass .Fingerprint.LatestStatus }}" href="/embed/fingerprint?site={{ queryEscape $.Site.Slug }}&amp;target={{ queryEscape .Slug }}" title="查看数字指纹 Top-K 候选排名（结果仅供参考）">
                      <span class="check-text">
                        <span class="check-key">指纹最像 </span>
                        <span class="check-value">{{ if .Fingerprint.TopCandidate }}{{ .Fingerprint.TopCandidate }}{{ else }}待测{{ end }}</span>
                      </span>
                    </a>
                    {{- end }}
                    {{- if and .Pelican (not .Pelican.LatestRunUUID) }}
                    <a class="check st-muted" href="/embed/pelican?site={{ queryEscape $.Site.Slug }}&amp;target={{ queryEscape .Slug }}">
                      <span class="check-text"><span class="check-key">画鹈鹕 </span><span class="check-state">待测</span></span>
                    </a>
                    {{- end }}
                    {{- if and (not .Candy) (not .Fingerprint) (not .Pelican) }}
                    <span class="st-muted">--</span>
                    {{- end }}
                  </div>
                </div>
              </td>
              {{- end }}
              <td class="cell-trend" data-label="{{ trendLabel .TrendWindowMinutes }}">
                <div class="trend-caption">{{ trendLabel .TrendWindowMinutes }}</div>
                <div class="trend-slot">
                <div class="trend-grid" role="img" aria-label="{{ .Name }} {{ trendLabel .TrendWindowMinutes }}">
                  {{- $window := .TrendWindowMinutes }}
                  {{- range .Trend }}
                  <span class="tcell"><span class="tbar {{ .Tone }}"></span><span class="tip"><span class="tip-time">{{ bucketClock .Timestamp $window }}</span><span class="tip-detail">{{ bucketDetail .SampleCount .SuccessCount .SuccessRate }}</span></span></span>
                  {{- end }}
                </div>
                </div>
              </td>
            </tr>
            {{- end }}
          </tbody>
        </table>
      </div>
      {{- end }}
    </main>

    <footer class="ftr">
      <div class="legend">
        <span class="legend-item"><span class="legend-swatch" style="background:#17803d"></span>正常</span>
        <span class="legend-item"><span class="legend-swatch" style="background:#b4690e"></span>波动</span>
        <span class="legend-item"><span class="legend-swatch" style="background:#d93036"></span>异常</span>
        <span class="legend-item"><span class="legend-swatch" style="background:var(--none)"></span>无采样</span>
        <span>· 左→右：窗口起点至现在</span>
        {{- if gt .Site.RefreshSeconds 0 }}<span>· 每 {{ .Site.RefreshSeconds }}s 刷新</span>{{- end }}
      </div>
      <div>最后数据时间：{{ fmtTimePtr .LatestData }}{{- if .Site.ShowIdentityColumn }} · 指纹排名仅供参考{{- end }}</div>
    </footer>
  </div>
</div>
</body>
</html>`

const tplCandy = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{ .TargetName }} · Candy 智力检测</title>
<style>` + sharedCSS + `</style>
</head>
<body class="{{ themeClass .Site.Theme }}">
<div class="wrap">
  <a class="back-link" href="/embed?site={{ queryEscape .Site.Slug }}">&#8592; 返回 {{ .Site.Title }}</a>
  <div class="sub-panel">
    <header class="hdr">
      <div>
        <div class="eyebrow">CANDY LOGIC BENCHMARK</div>
        <h1 class="title">{{ .TargetName }} · Candy 逻辑题历史</h1>
      </div>
      <div class="meta-bar">
        <span class="fam-tag {{ familyClass .Family }}">{{ familyLabel .Family }}</span>
        {{- if .ModelName }}<span class="pill num">{{ .ModelName }}</span>{{- end }}
        <span class="pill num">有效通过 {{ .PassedCount }}/{{ .TotalCount }}</span>
      </div>
    </header>

    <main>
      {{- if eq (len .Runs) 0 }}
      <div class="empty">暂无 Candy 检测历史记录。</div>
      {{- else }}
      <div class="strip-48" aria-label="最近检测状态条">
        {{- range .Runs }}
        <span class="strip-cell {{ statusClass .Status }}" title="{{ fmtTimeShort .ObservedAt }} · {{ statusLabel .Status }} · {{ .Summary }}"></span>
        {{- end }}
      </div>
      {{- range .Runs }}
      <details class="run-item">
        <summary>
          <div class="intel-cell">
            <span class="badge {{ statusClass .Status }}">{{ statusLabel .Status }}</span>
            <strong>{{ .Summary }}</strong>
            {{- if .ReasoningEffort }}<span class="pill">{{ .ReasoningEffort }}</span>{{- end }}
          </div>
          <div class="meta-bar num">
            <span>{{ fmtMS .LatencyMS }}</span>
            <span>{{ fmtTimeShort .ObservedAt }}</span>
          </div>
        </summary>
        <div class="run-body">
          <div class="meta-bar num">
            <span>模型：{{ .ModelName }}</span>
            <span>首字：{{ fmtMS .FirstTokenMS }}</span>
            <span>输出 Tokens：{{ .OutputTokens }}</span>
            <span>时间：{{ fmtTimeFull .ObservedAt }}</span>
          </div>
          {{- if .ErrorMessage }}
          <pre class="answer-box">{{ .ErrorMessage }}</pre>
          {{- else if .Answer }}
          <pre class="answer-box">{{ .Answer }}</pre>
          {{- else }}
          <pre class="answer-box">（无返回文本）</pre>
          {{- end }}
        </div>
      </details>
      {{- end }}
      {{- end }}
    </main>
  </div>
</div>
</body>
</html>`

const tplFingerprint = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{ .TargetName }} · 数字指纹排名</title>
<style>` + sharedCSS + `</style>
</head>
<body class="{{ themeClass .Site.Theme }}">
<div class="wrap">
  <a class="back-link" href="/embed?site={{ queryEscape .Site.Slug }}">&#8592; 返回 {{ .Site.Title }}</a>
  <div class="notice">
    说明：基于 3 轮 1-355 随机整数分布统计特征与 lm-detector 参考库比对。排名与概率仅供参考，不作为绝对身份证明。
  </div>

  <div class="sub-panel">
    <header class="hdr">
      <div>
        <div class="eyebrow">NUMERIC FINGERPRINT (LMFPD)</div>
        <h1 class="title">{{ .TargetName }} · 数字指纹历史</h1>
      </div>
      <div class="meta-bar">
        <span class="fam-tag {{ familyClass .Family }}">{{ familyLabel .Family }}</span>
        {{- if .ModelName }}<span class="pill num">{{ .ModelName }}</span>{{- end }}
        <span class="pill num">记录 {{ len .Runs }} 条</span>
      </div>
    </header>

    <main>
      {{- if eq (len .Runs) 0 }}
      <div class="empty">暂无数字指纹检测历史记录。</div>
      {{- else }}
      <div class="strip-48" aria-label="最近指纹状态条">
        {{- range .Runs }}
        <span class="strip-cell {{ statusClass .Status }}" title="{{ fmtTimeShort .ObservedAt }} · {{ statusLabel .Status }} · {{ .Summary }}"></span>
        {{- end }}
      </div>
      {{- range .Runs }}
      <details class="run-item">
        <summary>
          <div class="intel-cell">
            <span class="badge {{ statusClass .Status }}">{{ statusLabel .Status }}</span>
            <strong>{{ .Summary }}</strong>
            {{- if gt .ClaimedRank 0 }}<span class="pill num">声称模型排名 #{{ .ClaimedRank }}</span>{{- end }}
          </div>
          <div class="meta-bar num">
            <span>{{ fmtMS .LatencyMS }}</span>
            <span>{{ fmtTimeShort .ObservedAt }}</span>
          </div>
        </summary>
        <div class="run-body">
          <div class="meta-bar num">
            <span>测试模型：{{ .ModelName }}</span>
            <span>输出 Tokens：{{ .OutputTokens }}</span>
            <span>对照模型：{{ .ClaimedModel }}</span>
            {{- if .FamilyPrediction }}<span>预测家族：{{ .FamilyPrediction }}</span>{{- end }}
            <span>时间：{{ fmtTimeFull .ObservedAt }}</span>
          </div>
          {{- if .ErrorMessage }}
          <pre class="answer-box">{{ .ErrorMessage }}</pre>
          {{- end }}
          {{- if gt (len .TopCandidates) 0 }}
          <div class="cand-list">
            {{- range .TopCandidates }}
            <div class="cand-row">
              <span class="num st-muted">#{{ .Rank }}</span>
              <span class="num">{{ .DisplayName }}</span>
              <div class="prob-track"><span class="prob-fill" style="{{ probWidthStyle .Probability }}"></span></div>
              <span class="num">{{ fmtProb .Probability }}</span>
            </div>
            {{- end }}
          </div>
          {{- end }}
        </div>
      </details>
      {{- end }}
      {{- end }}
    </main>
  </div>
</div>
</body>
</html>`

const tplPelican = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{ .TargetName }} · 画鹈鹕画廊</title>
<style>` + sharedCSS + `</style>
</head>
<body class="{{ themeClass .Site.Theme }}">
<div class="wrap">
  <a class="back-link" href="/embed?site={{ queryEscape .Site.Slug }}">&#8592; 返回 {{ .Site.Title }}</a>
  <div class="notice">
    安全隔离：所有模型生成的 SVG 均通过独立端点严格 CSP + 双层空 sandbox iframe 渲染，完全禁止脚本执行与外部资源加载。
  </div>

  <div class="sub-panel">
    <header class="hdr">
      <div>
        <div class="eyebrow">PELICAN SVG GALLERY</div>
        <h1 class="title">{{ .TargetName }} · 画鹈鹕沙箱画廊</h1>
      </div>
      <div class="meta-bar">
        <span class="fam-tag {{ familyClass .Family }}">{{ familyLabel .Family }}</span>
        {{- if .ModelName }}<span class="pill num">{{ .ModelName }}</span>{{- end }}
        <span class="pill num">共 {{ len .Items }} 幅{{- if gt (len .DayGroups) 0 }} · {{ len .DayGroups }} 天{{- end }}</span>
      </div>
    </header>

    <main>
      {{- if eq (len .DayGroups) 0 }}
      <div class="empty">暂无已生成的画鹈鹕 SVG 画面。</div>
      {{- else }}
      <div class="gallery-stage">
        <input class="col-radio" type="radio" name="pelican-cols" id="cols-2">
        <input class="col-radio" type="radio" name="pelican-cols" id="cols-3" checked>
        <input class="col-radio" type="radio" name="pelican-cols" id="cols-4">

        <div class="gallery-toolbar">
          <nav class="day-nav" aria-label="按天跳转">
            {{- range .DayGroups }}
            <a class="day-pill" href="#day-{{ .Date }}">
              <span>{{ .ShortLabel }}</span>
              <span class="day-pill-count num">{{ len .Items }}</span>
            </a>
            {{- end }}
          </nav>
          <div class="col-switch" aria-label="每行排列列数">
            <span class="col-switch-label">每行</span>
            <label for="cols-2" title="每行 2 幅">2</label>
            <label for="cols-3" title="每行 3 幅">3</label>
            <label for="cols-4" title="每行 4 幅">4</label>
          </div>
        </div>

        <div class="day-groups">
          {{- range .DayGroups }}
          <details class="day-group" id="day-{{ .Date }}" open>
            <summary class="day-header">
              <div class="day-heading">
                <span class="day-chevron" aria-hidden="true"></span>
                <span class="day-label{{ if eq .Label .Date }} num{{ end }}">{{ .Label }}</span>
                {{- if ne .Label .Date }}
                <span class="day-sub num">{{ .Date }} · {{ .Weekday }}</span>
                {{- else }}
                <span class="day-sub">{{ .Weekday }}</span>
                {{- end }}
              </div>
              <span class="pill num">{{ len .Items }} 幅</span>
            </summary>
            <div class="gallery-grid">
              {{- range .Items }}
              <article class="gallery-card">
                <iframe class="gallery-frame" sandbox="" loading="lazy" referrerpolicy="no-referrer" title="Pelican SVG {{ .RunUUID }}" src="/embed/pelican/{{ queryEscape .RunUUID }}?site={{ queryEscape $.Site.Slug }}"></iframe>
                <div class="gallery-meta">
                  <span class="gallery-meta-left num" title="{{ .ModelName }} · {{ fmtMS .LatencyMS }}">{{ .ModelName }} · {{ fmtMS .LatencyMS }}</span>
                  <span class="gallery-meta-right">
                    <span class="num" title="{{ fmtTimeFull .ObservedAt }}">{{ fmtClock .ObservedAt }}</span>
                    <a class="gallery-open" href="/embed/pelican/{{ queryEscape .RunUUID }}?site={{ queryEscape $.Site.Slug }}" target="_blank" rel="noopener noreferrer" title="新窗口查看大图">大图 &#8599;</a>
                  </span>
                </div>
              </article>
              {{- end }}
            </div>
          </details>
          {{- end }}
        </div>
      </div>
      {{- end }}
    </main>
  </div>
</div>
</body>
</html>`

type renderedTemplates struct {
	overview    *template.Template
	candy       *template.Template
	fingerprint *template.Template
	pelican     *template.Template
}

var ssrTemplates = mustParseTemplates()

func mustParseTemplates() *renderedTemplates {
	return &renderedTemplates{
		overview:    template.Must(template.New("overview").Funcs(embedFuncMap).Parse(tplOverview)),
		candy:       template.Must(template.New("candy").Funcs(embedFuncMap).Parse(tplCandy)),
		fingerprint: template.Must(template.New("fingerprint").Funcs(embedFuncMap).Parse(tplFingerprint)),
		pelican:     template.Must(template.New("pelican").Funcs(embedFuncMap).Parse(tplPelican)),
	}
}
