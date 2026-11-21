package embed

import (
	"fmt"
	"html/template"
	"math"
	"net/url"
	"strings"
	"time"
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
	"bucketTitle": func(ts time.Time, samples, okCount int, rate float64) string {
		label := ts.Local().Format("15:04")
		if samples <= 0 {
			return fmt.Sprintf("%s 无采样", label)
		}
		return fmt.Sprintf("%s 可用率 %.0f%% (%d/%d)", label, rate, okCount, samples)
	},
	"themeClass": func(theme string) string {
		return "theme-" + normalizeTheme(theme)
	},
}

const sharedCSS = `
:root {
  --bg: #f6f2ea;
  --bg-accent: radial-gradient(circle at 12% 0%, rgba(200, 82, 50, 0.055), transparent 44%), radial-gradient(circle at 88% 0%, rgba(23, 114, 82, 0.05), transparent 42%);
  --surface: #fffefa;
  --surface-alt: #f3eee4;
  --surface-hover: #f8f4ec;
  --border: #e5ded0;
  --border-strong: #d4cab6;
  --ink: #22201c;
  --ink-muted: #625d54;
  --ink-subtle: #8b857a;
  --accent: #c85232;
  --accent-soft: #f9eae4;
  --ok: #137a52;
  --ok-bg: #e2f4ec;
  --warn: #b46910;
  --warn-bg: #faeed7;
  --down: #c4362f;
  --down-bg: #fce6e4;
  --none: #d8d0c1;
  --shadow: 0 10px 30px rgba(45, 38, 26, 0.055), 0 1px 2px rgba(45, 38, 26, 0.04);
  --radius: 16px;
}
body.theme-light {
  --bg: #f4f7fb;
  --bg-accent: radial-gradient(circle at 10% 0%, rgba(15, 118, 110, 0.06), transparent 45%);
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
  --ok: #15803d;
  --ok-bg: #dcfce7;
  --warn: #b45309;
  --warn-bg: #fef3c7;
  --down: #dc2626;
  --down-bg: #fee2e2;
  --none: #d8e1ec;
  --shadow: 0 8px 26px rgba(15, 23, 42, 0.05), 0 1px 2px rgba(15, 23, 42, 0.03);
}
body.theme-dark {
  --bg: #0c1016;
  --bg-accent: radial-gradient(circle at 15% 0%, rgba(63, 185, 132, 0.09), transparent 45%), radial-gradient(circle at 85% 0%, rgba(56, 139, 253, 0.07), transparent 42%);
  --surface: #131922;
  --surface-alt: #1a222e;
  --surface-hover: #1e2736;
  --border: #263142;
  --border-strong: #36455c;
  --ink: #e9eff6;
  --ink-muted: #98a6b8;
  --ink-subtle: #6f7f94;
  --accent: #3fb984;
  --accent-soft: rgba(63, 185, 132, 0.14);
  --ok: #34d399;
  --ok-bg: rgba(52, 211, 153, 0.14);
  --warn: #f0b429;
  --warn-bg: rgba(240, 180, 41, 0.16);
  --down: #f87171;
  --down-bg: rgba(248, 113, 113, 0.16);
  --none: #2c3849;
  --shadow: 0 12px 32px rgba(0, 0, 0, 0.35);
}
* { box-sizing: border-box; }
html, body {
  margin: 0;
  padding: 0;
  background-color: var(--bg);
  background-image: var(--bg-accent);
  color: var(--ink);
  font-family: "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-size: 13px;
  line-height: 1.5;
  -webkit-font-smoothing: antialiased;
}
a { color: inherit; text-decoration: none; }
.wrap {
  max-width: 1280px;
  margin: 0 auto;
  padding: 16px 18px 22px;
}
.shell {
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
  gap: 14px;
  flex-wrap: wrap;
  padding: 18px 22px 15px;
  border-bottom: 1px solid var(--border);
  background: linear-gradient(180deg, var(--surface) 0%, var(--surface-hover) 100%);
}
.eyebrow {
  font-size: 10px;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--accent);
  font-weight: 700;
}
.title {
  margin: 3px 0 0;
  font-family: "Songti SC", "Noto Serif SC", "STSong", Georgia, serif;
  font-size: 22px;
  font-weight: 700;
  letter-spacing: 0.01em;
  line-height: 1.25;
}
body.theme-light .title,
body.theme-dark .title {
  font-family: "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", system-ui, sans-serif;
  letter-spacing: -0.02em;
}
.meta-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  color: var(--ink-muted);
  font-size: 11.5px;
}
.pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 10px;
  border-radius: 999px;
  background: var(--surface);
  border: 1px solid var(--border);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
@keyframes livePulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.45; transform: scale(0.84); }
}
.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--ok);
  display: inline-block;
  box-shadow: 0 0 0 3px var(--ok-bg);
  animation: livePulse 2.4s ease-in-out infinite;
}
.table-wrap {
  width: 100%;
  overflow-x: auto;
}
table.perf-table {
  width: 100%;
  border-collapse: collapse;
}
table.perf-table thead th {
  text-align: left;
  font-size: 10.5px;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--ink-subtle);
  background: var(--surface-alt);
  border-bottom: 1px solid var(--border);
  padding: 10px 14px;
  white-space: nowrap;
}
table.perf-table thead th.col-num { text-align: right; }
table.perf-table tbody td {
  padding: 13px 14px;
  border-bottom: 1px solid var(--border);
  vertical-align: middle;
}
table.perf-table tbody tr:last-child td { border-bottom: none; }
table.perf-table tbody tr {
  transition: background-color 0.14s ease;
}
table.perf-table tbody tr:hover {
  background: var(--surface-hover);
}
.num {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
td.cell-num {
  text-align: right;
  font-size: 13px;
  font-weight: 600;
}
.unit {
  font-size: 10.5px;
  font-weight: 500;
  color: var(--ink-subtle);
  margin-left: 2px;
}
.target-cell {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  max-width: 100%;
}
.fam-tag {
  display: inline-flex;
  align-items: center;
  padding: 2px 7px;
  border-radius: 6px;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  flex-shrink: 0;
}
.fam-claude { background: rgba(194, 96, 48, 0.14); color: #b54e20; }
.fam-codex { background: rgba(14, 124, 134, 0.14); color: #0a6f78; }
.fam-grok { background: rgba(61, 90, 128, 0.15); color: #365277; }
.fam-domestic { background: rgba(114, 82, 173, 0.14); color: #67449a; }
.fam-other { background: var(--surface-alt); color: var(--ink-muted); }
body.theme-dark .fam-claude { color: #f3a177; }
body.theme-dark .fam-codex { color: #5bd6e0; }
body.theme-dark .fam-grok { color: #8cb4e6; }
body.theme-dark .fam-domestic { color: #c4a7f2; }
.target-name {
  font-weight: 700;
  font-size: 13.5px;
  color: var(--ink);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.st-ok { color: var(--ok); }
.st-warn { color: var(--warn); }
.st-down { color: var(--down); }
.st-muted { color: var(--ink-subtle); }
.rate-pill {
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
  padding: 2px 8px;
  border-radius: 7px;
  font-weight: 700;
  font-size: 12.5px;
}
.rate-pill.st-ok { background: var(--ok-bg); color: var(--ok); }
.rate-pill.st-warn { background: var(--warn-bg); color: var(--warn); }
.rate-pill.st-down { background: var(--down-bg); color: var(--down); }
.rate-pill.st-muted { background: var(--surface-alt); color: var(--ink-subtle); }

.intel-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}
.intel-stack {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  min-width: 0;
}
.badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 8px;
  border-radius: 6px;
  font-size: 11px;
  font-weight: 600;
  border: 1px solid transparent;
  white-space: nowrap;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  transition: transform 0.12s ease, filter 0.12s ease;
}
.badge::before {
  content: "";
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: currentColor;
  flex-shrink: 0;
}
.badge.st-ok { background: var(--ok-bg); color: var(--ok); }
.badge.st-warn { background: var(--warn-bg); color: var(--warn); }
.badge.st-down { background: var(--down-bg); color: var(--down); }
.badge.st-muted { background: var(--surface-alt); color: var(--ink-muted); border-color: var(--border); }
a.badge:hover { filter: brightness(0.95); transform: translateY(-0.5px); }

.pelican-thumb {
  display: inline-block;
  position: relative;
  width: 44px;
  height: 33px;
  border-radius: 6px;
  border: 1px solid var(--border-strong);
  background: #f4f7f4;
  overflow: hidden;
  flex-shrink: 0;
  vertical-align: middle;
  transition: border-color 0.15s ease, transform 0.15s ease;
}
.pelican-thumb:hover {
  border-color: var(--accent);
  transform: scale(1.06);
}
.pelican-thumb iframe {
  position: absolute;
  top: 0;
  left: 0;
  width: 440px;
  height: 330px;
  border: 0;
  transform: scale(0.1);
  transform-origin: 0 0;
  pointer-events: none;
}

/* 可用率 60 分钟热力条：更宽、更疏朗、层级更分明 */
th.col-trend,
td.cell-trend {
  width: 34%;
  min-width: 280px;
}
.trend-box {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 100%;
}
.trend-grid {
  display: grid;
  grid-template-columns: repeat(60, minmax(2.5px, 1fr));
  gap: 2.5px;
  width: 100%;
  height: 24px;
  align-items: center;
}
.tbar {
  display: block;
  height: 22px;
  border-radius: 2.5px;
  background: var(--none);
  transition: transform 0.12s ease, opacity 0.12s ease;
}
.tbar:hover {
  transform: scaleY(1.18);
  opacity: 1 !important;
}
.tbar.ok { background: var(--ok); opacity: 0.92; }
.tbar.warn { background: var(--warn); opacity: 0.95; }
.tbar.down { background: var(--down); opacity: 0.95; }
.tbar.none {
  height: 11px;
  border-radius: 2px;
  background: var(--none);
  opacity: 0.52;
}
.trend-axis {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 9.5px;
  color: var(--ink-subtle);
  font-variant-numeric: tabular-nums;
  line-height: 1;
}

.ftr {
  padding: 11px 22px;
  border-top: 1px solid var(--border);
  background: var(--surface-alt);
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 16px;
  font-size: 11px;
  color: var(--ink-subtle);
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
  padding: 44px 18px;
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
.strip-cell.st-ok { background: var(--ok); }
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
.gallery {
  display: flex;
  gap: 14px;
  overflow-x: auto;
  scroll-snap-type: x mandatory;
  padding: 16px;
}
.gallery-card {
  flex: 0 0 340px;
  scroll-snap-align: start;
  background: var(--surface-alt);
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}
.gallery-frame {
  width: 100%;
  height: 255px;
  border: 0;
  background: #f4f7f4;
  display: block;
}
.gallery-meta {
  padding: 10px 13px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  font-size: 11px;
  color: var(--ink-muted);
  border-top: 1px solid var(--border);
}

/* 平板尺寸：稍微紧凑内边距，仍保持宽敞的 60 分钟热力条 */
@media (max-width: 1024px) {
  .wrap { padding: 12px; }
  .hdr { padding: 14px 16px 12px; }
  table.perf-table thead th,
  table.perf-table tbody td { padding: 10px 10px; }
  th.col-trend, td.cell-trend { min-width: 236px; }
  .trend-grid { gap: 2px; }
  .unit { display: none; }
}

/* 手机端 / 窄侧边栏 iframe：零横向滚动，纯 CSS 自动转为精致多端卡片布局 */
@media (max-width: 760px) {
  .wrap { padding: 10px; }
  .hdr { padding: 14px 14px 12px; align-items: flex-start; }
  .title { font-size: 18px; }
  .ftr { padding: 10px 14px; }
  .table-wrap { overflow-x: visible; }
  table.perf-table,
  table.perf-table tbody {
    display: block;
    width: 100%;
  }
  table.perf-table thead { display: none; }
  table.perf-table tbody tr {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px 10px;
    padding: 14px;
    border-bottom: 1px solid var(--border);
  }
  table.perf-table tbody tr:hover { background: transparent; }
  table.perf-table tbody td {
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
  }
  table.perf-table tbody td.cell-metric {
    background: var(--surface-alt);
    border: 1px solid var(--border);
    border-radius: 9px;
    padding: 7px 9px;
    text-align: left;
  }
  table.perf-table tbody td.cell-metric::before {
    content: attr(data-label);
    display: block;
    font-size: 10px;
    font-weight: 600;
    color: var(--ink-subtle);
    margin-bottom: 2px;
    font-family: "PingFang SC", "Microsoft YaHei", sans-serif;
  }
  table.perf-table tbody td.cell-intel {
    grid-column: 1 / -1;
    padding-top: 2px;
  }
  .intel-stack {
    flex-direction: row;
    flex-wrap: wrap;
    gap: 6px;
  }
  table.perf-table tbody td.cell-trend {
    grid-column: 1 / -1;
    min-width: 0;
    width: 100%;
    padding-top: 4px;
  }
  table.perf-table tbody td.cell-trend::before {
    content: attr(data-label);
    display: block;
    font-size: 10px;
    font-weight: 600;
    color: var(--ink-subtle);
    margin-bottom: 5px;
  }
  .trend-grid {
    grid-template-columns: repeat(60, minmax(2px, 1fr));
    gap: 2px;
    height: 22px;
  }
  .tbar { height: 20px; border-radius: 2px; }
  .tbar.none { height: 10px; }
  .cand-row {
    grid-template-columns: 28px minmax(110px, 1fr) 76px 48px;
    gap: 6px;
  }
  .gallery-card { flex: 0 0 285px; }
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
        <div class="eyebrow">{{ .Site.Eyebrow }}</div>
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
        <table class="perf-table">
          <thead>
            <tr>
              <th>渠道</th>
              <th class="col-num">TPS</th>
              <th class="col-num">首字 TTFT</th>
              <th class="col-num">平均延迟</th>
              <th class="col-num">24h 成功率</th>
              {{- if .Site.ShowIdentityColumn }}
              <th>模型检测</th>
              {{- end }}
              <th class="col-trend">近 60 分钟可用率</th>
            </tr>
          </thead>
          <tbody>
            {{- range .Targets }}
            <tr>
              <td class="cell-channel" data-label="渠道">
                <div class="target-cell">
                  <span class="fam-tag {{ familyClass .Family }}">{{ familyLabel .Family }}</span>
                  <span class="target-name" title="{{ .Name }}">{{ .Name }}</span>
                </div>
              </td>
              <td class="num cell-num cell-metric" data-label="TPS">
                {{ fmtFloat1 .Performance.TPS }}{{- if gt .Performance.TPS 0.0 }}<span class="unit">t/s</span>{{- end }}
              </td>
              <td class="num cell-num cell-metric" data-label="首字 TTFT">{{ fmtMS .Performance.AvgFirstTokenMS }}</td>
              <td class="num cell-num cell-metric" data-label="平均延迟">{{ fmtMS .Performance.AvgLatencyMS }}</td>
              <td class="num cell-num cell-rate" data-label="24h 成功率">
                <span class="rate-pill {{ rateClass .Performance.SuccessRate .Performance.SampleCount }}">
                  {{ fmtPct .Performance.SuccessRate .Performance.SampleCount }}
                </span>
              </td>
              {{- if $.Site.ShowIdentityColumn }}
              <td class="cell-intel" data-label="模型检测">
                <div class="intel-cell">
                  {{- if .Pelican }}
                    {{- if .Pelican.LatestRunUUID }}
                    <a class="pelican-thumb" href="/embed/pelican?site={{ queryEscape $.Site.Slug }}&amp;target={{ queryEscape .Slug }}" title="查看最近画鹈鹕 SVG 沙箱画廊">
                      <iframe sandbox="" loading="lazy" tabindex="-1" title="Pelican Preview" src="/embed/pelican/{{ queryEscape .Pelican.LatestRunUUID }}?site={{ queryEscape $.Site.Slug }}"></iframe>
                    </a>
                    {{- else }}
                    <a class="badge st-muted" href="/embed/pelican?site={{ queryEscape $.Site.Slug }}&amp;target={{ queryEscape .Slug }}" title="查看画鹈鹕记录">鹈鹕 待测</a>
                    {{- end }}
                  {{- end }}
                  {{- if or .Candy .Fingerprint }}
                  <div class="intel-stack">
                    {{- if .Candy }}
                    <a class="badge {{ statusClass .Candy.LatestStatus }}" href="/embed/candy?site={{ queryEscape $.Site.Slug }}&amp;target={{ queryEscape .Slug }}" title="查看最近 48 次 Candy 逻辑题详情">
                      Candy {{ if gt .Candy.Checked24h 0 }}{{ .Candy.Passed24h }}/{{ .Candy.Checked24h }}{{ else }}待测{{ end }}
                    </a>
                    {{- end }}
                    {{- if .Fingerprint }}
                    <a class="badge {{ statusClass .Fingerprint.LatestStatus }}" href="/embed/fingerprint?site={{ queryEscape $.Site.Slug }}&amp;target={{ queryEscape .Slug }}" title="查看数字指纹 Top-K 候选排名（仅供参考）">
                      {{- if .Fingerprint.TopCandidate }}
                        指纹 {{ if gt .Fingerprint.ClaimedRank 0 }}#{{ .Fingerprint.ClaimedRank }} {{ end }}{{ .Fingerprint.TopCandidate }}
                      {{- else }}
                        指纹 待测
                      {{- end }}
                    </a>
                    {{- end }}
                  </div>
                  {{- end }}
                  {{- if and (not .Candy) (not .Fingerprint) (not .Pelican) }}
                  <span class="st-muted">--</span>
                  {{- end }}
                </div>
              </td>
              {{- end }}
              <td class="cell-trend" data-label="近 60 分钟可用率">
                <div class="trend-box">
                  <div class="trend-grid" aria-label="近 60 分钟逐分钟可用率">
                    {{- range .Trend }}
                    <span class="tbar {{ .Tone }}" title="{{ bucketTitle .Timestamp .SampleCount .SuccessCount .SuccessRate }}"></span>
                    {{- end }}
                  </div>
                  <div class="trend-axis">
                    <span>60 分钟前</span>
                    <span>现在</span>
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
        <span class="legend-item"><span class="legend-swatch" style="background:var(--ok)"></span>正常</span>
        <span class="legend-item"><span class="legend-swatch" style="background:var(--warn)"></span>波动</span>
        <span class="legend-item"><span class="legend-swatch" style="background:var(--down)"></span>异常</span>
        <span class="legend-item"><span class="legend-swatch" style="background:var(--none)"></span>无采样</span>
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
        <span class="pill num">共 {{ len .Items }} 幅</span>
      </div>
    </header>

    <main>
      {{- if eq (len .Items) 0 }}
      <div class="empty">暂无已生成的画鹈鹕 SVG 画面。</div>
      {{- else }}
      <div class="gallery">
        {{- range .Items }}
        <article class="gallery-card">
          <iframe class="gallery-frame" sandbox="" loading="lazy" title="Pelican SVG {{ .RunUUID }}" src="/embed/pelican/{{ queryEscape .RunUUID }}?site={{ queryEscape $.Site.Slug }}"></iframe>
          <div class="gallery-meta">
            <span class="num">{{ .ModelName }} · {{ fmtMS .LatencyMS }}</span>
            <span class="num">{{ fmtTimeShort .ObservedAt }}</span>
          </div>
        </article>
        {{- end }}
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
