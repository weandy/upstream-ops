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
  --surface: #fffdf9;
  --surface-alt: #f1ece2;
  --border: #e4dccd;
  --border-strong: #d3c8b4;
  --ink: #1e2420;
  --ink-muted: #616a63;
  --ink-subtle: #879088;
  --accent: #1d6f52;
  --accent-soft: #e3f1eb;
  --ok: #1f7a53;
  --ok-bg: #e4f3ec;
  --warn: #b46914;
  --warn-bg: #faeed9;
  --down: #b43832;
  --down-bg: #fbe6e4;
  --none: #dfd8ca;
  --shadow: 0 1px 2px rgba(28, 35, 31, 0.04);
  --radius: 10px;
}
body.theme-light {
  --bg: #f7f9fc;
  --surface: #ffffff;
  --surface-alt: #f1f5f9;
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
  --down: #b91c1c;
  --down-bg: #fee2e2;
  --none: #e2e8f0;
}
body.theme-dark {
  --bg: #0d1117;
  --surface: #151b23;
  --surface-alt: #1c2430;
  --border: #263040;
  --border-strong: #364458;
  --ink: #e6edf3;
  --ink-muted: #9aa7b8;
  --ink-subtle: #6e7d91;
  --accent: #3fb984;
  --accent-soft: rgba(63, 185, 132, 0.14);
  --ok: #3fb950;
  --ok-bg: rgba(63, 185, 80, 0.14);
  --warn: #e3b341;
  --warn-bg: rgba(227, 179, 65, 0.16);
  --down: #f85149;
  --down-bg: rgba(248, 81, 73, 0.16);
  --none: #273140;
  --shadow: none;
}
* { box-sizing: border-box; }
html, body {
  margin: 0;
  padding: 0;
  background: var(--bg);
  color: var(--ink);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Noto Sans SC", "Hiragino Sans GB", sans-serif;
  font-size: 13px;
  line-height: 1.5;
  -webkit-font-smoothing: antialiased;
}
a { color: inherit; text-decoration: none; }
.wrap {
  max-width: 1180px;
  margin: 0 auto;
  padding: 16px 18px 20px;
}
.hdr {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.eyebrow {
  font-size: 10px;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--ink-subtle);
  font-weight: 600;
}
.title {
  margin: 2px 0 0;
  font-size: 20px;
  font-weight: 700;
  letter-spacing: -0.01em;
}
.meta-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  color: var(--ink-muted);
  font-size: 12px;
}
.pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--surface);
  border: 1px solid var(--border);
  font-variant-numeric: tabular-nums;
}
.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--ok);
  display: inline-block;
}
.panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow);
  overflow: hidden;
}
.table-scroll {
  width: 100%;
  overflow-x: auto;
}
table {
  width: 100%;
  border-collapse: collapse;
  min-width: 760px;
}
thead th {
  text-align: left;
  font-size: 11px;
  font-weight: 600;
  color: var(--ink-muted);
  background: var(--surface-alt);
  border-bottom: 1px solid var(--border);
  padding: 9px 11px;
  white-space: nowrap;
}
tbody td {
  padding: 10px 11px;
  border-bottom: 1px solid var(--border);
  vertical-align: middle;
}
tbody tr:last-child td { border-bottom: none; }
tbody tr:hover { background: var(--surface-alt); }
.num {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.target-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 150px;
}
.fam-tag {
  display: inline-block;
  padding: 1px 6px;
  border-radius: 5px;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.03em;
  text-transform: uppercase;
  flex-shrink: 0;
}
.fam-claude { background: rgba(194, 106, 59, 0.16); color: #b85828; }
.fam-codex { background: rgba(33, 122, 84, 0.16); color: #1b6f4b; }
.fam-grok { background: rgba(61, 90, 128, 0.16); color: #365277; }
.fam-domestic { background: rgba(109, 76, 158, 0.16); color: #67449a; }
.fam-other { background: var(--surface-alt); color: var(--ink-muted); }
body.theme-dark .fam-claude { color: #f09b6d; }
body.theme-dark .fam-codex { color: #5cd39e; }
body.theme-dark .fam-grok { color: #8cb4e6; }
body.theme-dark .fam-domestic { color: #c4a7f2; }
.target-name { font-weight: 600; color: var(--ink); }
.st-ok { color: var(--ok); }
.st-warn { color: var(--warn); }
.st-down { color: var(--down); }
.st-muted { color: var(--ink-subtle); }
.badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 7px;
  border-radius: 6px;
  font-size: 11px;
  font-weight: 600;
  border: 1px solid transparent;
  white-space: nowrap;
}
.badge.st-ok { background: var(--ok-bg); color: var(--ok); }
.badge.st-warn { background: var(--warn-bg); color: var(--warn); }
.badge.st-down { background: var(--down-bg); color: var(--down); }
.badge.st-muted { background: var(--surface-alt); color: var(--ink-muted); border-color: var(--border); }
a.badge:hover { filter: brightness(0.95); text-decoration: underline; }
.intel-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.pelican-thumb {
  display: inline-block;
  position: relative;
  width: 40px;
  height: 30px;
  border-radius: 5px;
  border: 1px solid var(--border-strong);
  background: #f4f7f4;
  overflow: hidden;
  flex-shrink: 0;
  vertical-align: middle;
}
.pelican-thumb:hover { border-color: var(--accent); }
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
.trend-grid {
  display: grid;
  grid-template-columns: repeat(90, minmax(2px, 1fr));
  gap: 1px;
  width: 196px;
  height: 22px;
  align-items: stretch;
}
.tbar {
  display: block;
  border-radius: 1px;
  background: var(--none);
}
.tbar.ok { background: var(--ok); }
.tbar.warn { background: var(--warn); }
.tbar.down { background: var(--down); }
.tbar.none { background: var(--none); opacity: 0.55; }
.ftr {
  margin-top: 10px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  font-size: 11px;
  color: var(--ink-subtle);
}
.empty {
  padding: 36px 16px;
  text-align: center;
  color: var(--ink-muted);
}
.back-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--accent);
  font-weight: 600;
  margin-bottom: 6px;
}
.back-link:hover { text-decoration: underline; }
.notice {
  background: var(--surface-alt);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 8px 12px;
  font-size: 12px;
  color: var(--ink-muted);
  margin-bottom: 12px;
}
.strip-48 {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  padding: 12px;
  border-bottom: 1px solid var(--border);
}
.strip-cell {
  width: 14px;
  height: 24px;
  border-radius: 3px;
  display: inline-block;
  background: var(--none);
}
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
  padding: 10px 14px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}
details.run-item > summary::-webkit-details-marker { display: none; }
details.run-item > summary:hover { background: var(--surface-alt); }
.run-body {
  padding: 10px 14px 14px;
  background: var(--surface-alt);
  border-top: 1px solid var(--border);
}
pre.answer-box {
  margin: 6px 0 0;
  padding: 10px;
  border-radius: 6px;
  background: var(--surface);
  border: 1px solid var(--border);
  white-space: pre-wrap;
  word-break: break-word;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
.cand-list {
  display: grid;
  gap: 6px;
  margin-top: 6px;
}
.cand-row {
  display: grid;
  grid-template-columns: 32px minmax(140px, 1.4fr) minmax(90px, 1fr) 54px;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
.cand-row meter {
  width: 100%;
  height: 10px;
}
.gallery {
  display: flex;
  gap: 14px;
  overflow-x: auto;
  scroll-snap-type: x mandatory;
  padding: 14px;
}
.gallery-card {
  flex: 0 0 340px;
  scroll-snap-align: start;
  background: var(--surface-alt);
  border: 1px solid var(--border);
  border-radius: 10px;
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
  padding: 9px 12px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  font-size: 11px;
  color: var(--ink-muted);
  border-top: 1px solid var(--border);
}
@media (max-width: 720px) {
  .wrap { padding: 12px; }
  .title { font-size: 17px; }
  .gallery-card { flex: 0 0 290px; }
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

  <main class="panel">
    {{- if eq (len .Targets) 0 }}
    <div class="empty">暂无可公开展示的监测渠道，请在管理台启用目标并勾选「对外 Embed 展示」。</div>
    {{- else }}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>渠道</th>
            <th>TPS</th>
            <th>首字 TTFT</th>
            <th>平均延迟</th>
            <th>24h 成功率</th>
            {{- if .Site.ShowIdentityColumn }}
            <th>模型检测</th>
            {{- end }}
            <th>近 90 分钟可用率</th>
          </tr>
        </thead>
        <tbody>
          {{- range .Targets }}
          <tr>
            <td>
              <div class="target-cell">
                <span class="fam-tag {{ familyClass .Family }}">{{ familyLabel .Family }}</span>
                <span class="target-name">{{ .Name }}</span>
              </div>
            </td>
            <td class="num">{{ fmtFloat1 .Performance.TPS }}</td>
            <td class="num">{{ fmtMS .Performance.AvgFirstTokenMS }}</td>
            <td class="num">{{ fmtMS .Performance.AvgLatencyMS }}</td>
            <td class="num {{ rateClass .Performance.SuccessRate .Performance.SampleCount }}">
              {{ fmtPct .Performance.SuccessRate .Performance.SampleCount }}
            </td>
            {{- if $.Site.ShowIdentityColumn }}
            <td>
              <div class="intel-cell">
                {{- if .Candy }}
                <a class="badge {{ statusClass .Candy.LatestStatus }}" href="/embed/candy?site={{ queryEscape $.Site.Slug }}&amp;target={{ queryEscape .Slug }}" title="查看最近 48 次 Candy 逻辑题详情">
                  糖果 {{ if gt .Candy.Checked24h 0 }}{{ .Candy.Passed24h }}/{{ .Candy.Checked24h }}{{ else }}待测{{ end }}
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
                {{- if .Pelican }}
                  {{- if .Pelican.LatestRunUUID }}
                  <a class="pelican-thumb" href="/embed/pelican?site={{ queryEscape $.Site.Slug }}&amp;target={{ queryEscape .Slug }}" title="查看最近画鹈鹕 SVG 沙箱画廊">
                    <iframe sandbox="" loading="lazy" tabindex="-1" title="Pelican Preview" src="/embed/pelican/{{ queryEscape .Pelican.LatestRunUUID }}?site={{ queryEscape $.Site.Slug }}"></iframe>
                  </a>
                  {{- else }}
                  <a class="badge st-muted" href="/embed/pelican?site={{ queryEscape $.Site.Slug }}&amp;target={{ queryEscape .Slug }}" title="查看画鹈鹕记录">鹈鹕 待测</a>
                  {{- end }}
                {{- end }}
                {{- if and (not .Candy) (not .Fingerprint) (not .Pelican) }}
                <span class="st-muted">--</span>
                {{- end }}
              </div>
            </td>
            {{- end }}
            <td>
              <div class="trend-grid" aria-label="近 90 分钟逐分钟可用率">
                {{- range .Trend }}
                <span class="tbar {{ .Tone }}" title="{{ bucketTitle .Timestamp .SampleCount .SuccessCount .SuccessRate }}"></span>
                {{- end }}
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
    <div>24h 性能指标与 90 分钟可用率基于主动流式测活采样{{- if gt .Site.RefreshSeconds 0 }} · 每 {{ .Site.RefreshSeconds }} 秒自动刷新{{- end }}</div>
    <div>最后数据时间：{{ fmtTimePtr .LatestData }}{{- if .Site.ShowIdentityColumn }} · 数字指纹排名仅供参考，不作为绝对身份证明{{- end }}</div>
  </footer>
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

  <main class="panel">
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

  <div class="notice">
    说明：基于 3 轮 1-355 随机整数分布统计特征与 lm-detector 参考库比对。排名与概率仅供参考，不作为绝对身份证明。
  </div>

  <main class="panel">
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
            <meter min="0" max="100" value="{{ probPct .Probability }}"></meter>
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

  <div class="notice">
    安全隔离：所有模型生成的 SVG 均通过独立端点严格 CSP + 双层空 sandbox iframe 渲染，完全禁止脚本执行与外部资源加载。
  </div>

  <main class="panel">
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
