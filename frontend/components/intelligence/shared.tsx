import { useEffect, useLayoutEffect, useRef, useState, type MouseEvent } from "react"
import { createPortal } from "react-dom"
import { getToken } from "@/lib/api"
import type {
  AvailabilityBucket,
  IntelligenceStatus,
  ProbeFamily,
  ProbeProtocol,
} from "@/lib/api-types"
import { cn } from "@/lib/utils"

export const FAMILY_META: Record<
  ProbeFamily,
  { label: string; badgeClass: string; dotClass: string }
> = {
  claude: {
    label: "Claude",
    badgeClass:
      "border-orange-500/25 bg-orange-500/10 text-orange-700 dark:text-orange-300",
    dotClass: "bg-orange-500",
  },
  codex: {
    label: "Codex",
    badgeClass:
      "border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
    dotClass: "bg-emerald-500",
  },
  grok: {
    label: "Grok",
    badgeClass:
      "border-sky-500/25 bg-sky-500/10 text-sky-700 dark:text-sky-300",
    dotClass: "bg-sky-500",
  },
  domestic: {
    label: "国产",
    badgeClass:
      "border-violet-500/25 bg-violet-500/10 text-violet-700 dark:text-violet-300",
    dotClass: "bg-violet-500",
  },
  other: {
    label: "通用",
    badgeClass:
      "border-border bg-muted/70 text-muted-foreground",
    dotClass: "bg-muted-foreground",
  },
}

export const PROTOCOL_LABELS: Record<ProbeProtocol, string> = {
  openai_chat: "OpenAI Chat",
  openai_responses: "OpenAI Responses",
  anthropic: "Anthropic",
}

export const REASONING_EFFORT_OPTIONS: Array<{ value: string; label: string }> = [
  { value: "omit", label: "不传（默认）" },
  { value: "none", label: "none" },
  { value: "minimal", label: "minimal" },
  { value: "low", label: "low" },
  { value: "medium", label: "medium" },
  { value: "high", label: "high" },
  { value: "xhigh", label: "xhigh" },
  { value: "max", label: "max" },
]

export function statusBadgeProps(status?: IntelligenceStatus | string): {
  label: string
  className: string
} {
  switch (status) {
    case "normal":
      return {
        label: "正常",
        className:
          "border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
      }
    case "suspected":
      return {
        label: "疑似异常",
        className:
          "border-amber-500/30 bg-amber-500/15 text-amber-700 dark:text-amber-300",
      }
    case "error":
      return {
        label: "探测出错",
        className:
          "border-rose-500/30 bg-rose-500/15 text-rose-700 dark:text-rose-300",
      }
    default:
      return {
        label: "待测",
        className: "border-border bg-muted/60 text-muted-foreground",
      }
  }
}

export function formatIntervalHuman(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "—"
  if (seconds < 60) return `${seconds} 秒`
  if (seconds % 3600 === 0) return `${seconds / 3600} 小时`
  if (seconds % 60 === 0) return `${seconds / 60} 分钟`
  return `${seconds} 秒`
}

export function formatSuccessRateClass(rate: number, samples: number): string {
  if (samples <= 0) return "text-muted-foreground"
  if (rate >= 95) return "text-emerald-600 dark:text-emerald-400"
  if (rate >= 80) return "text-amber-600 dark:text-amber-400"
  return "text-rose-600 dark:text-rose-400"
}

function bucketBarClass(tone: AvailabilityBucket["tone"], compact: boolean): string {
  const sizeClass = compact ? "h-[18px] rounded-[1.5px]" : "h-6 rounded-[2px]"
  switch (tone) {
    case "ok":
      return cn(sizeClass, "bg-emerald-600/90 dark:bg-emerald-400/90")
    case "warn":
      return cn(sizeClass, "bg-amber-500/95 dark:bg-amber-400/95")
    case "down":
      return cn(sizeClass, "bg-rose-500/95 dark:bg-rose-400/95")
    default:
      return cn(sizeClass, "bg-muted-foreground/22")
  }
}

function bucketTip(b: AvailabilityBucket, windowMinutes: number): { time: string; detail: string } {
  const d = new Date(b.timestamp)
  const hhmm = (value: Date) =>
    Number.isNaN(value.getTime())
      ? "--:--"
      : value.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })
  const minutes = windowMinutes > 0 ? windowMinutes : 60
  const bucketMs = (minutes * 60_000) / 60
  const time =
    Number.isNaN(d.getTime())
      ? "--:--"
      : bucketMs <= 60_000
        ? hhmm(d)
        : `${hhmm(d)}–${hhmm(new Date(d.getTime() + bucketMs))}`
  if (b.sample_count <= 0) return { time, detail: "无采样" }
  return {
    time,
    detail: `可用率 ${b.success_rate.toFixed(0)}% · ${b.success_count}/${b.sample_count}`,
  }
}

type TrendTip = {
  time: string
  detail: string
  rect: { left: number; top: number; width: number; height: number; bottom: number }
}

function TrendHoverCard({ tip }: { tip: TrendTip }) {
  const ref = useRef<HTMLDivElement>(null)
  const [size, setSize] = useState({ w: 112, h: 44 })
  useLayoutEffect(() => {
    const node = ref.current
    if (!node) return
    const w = node.offsetWidth
    const h = node.offsetHeight
    if (w !== size.w || h !== size.h) setSize({ w, h })
  }, [tip, size.w, size.h])

  const center = tip.rect.left + tip.rect.width / 2
  const pad = 8
  let left = center - size.w / 2
  if (left < pad) left = pad
  if (left + size.w > window.innerWidth - pad) {
    left = Math.max(pad, window.innerWidth - pad - size.w)
  }
  let top = tip.rect.top - size.h - 8
  let below = false
  if (top < pad) {
    top = tip.rect.bottom + 8
    below = true
  }
  let arrow = center - left
  if (arrow < 12) arrow = 12
  if (arrow > size.w - 12) arrow = size.w - 12

  return createPortal(
    <div
      ref={ref}
      className="pointer-events-none fixed z-[80]"
      style={{ left, top }}
    >
      <div className="min-w-[96px] rounded-lg border border-border bg-popover px-2.5 py-1.5 text-popover-foreground shadow-lg">
        <div className="font-mono text-[13px] font-semibold leading-tight tabular-nums">
          {tip.time}
        </div>
        <div className="mt-0.5 whitespace-nowrap text-[11px] leading-tight text-muted-foreground">
          {tip.detail}
        </div>
      </div>
      <span
        className={cn(
          "absolute h-2 w-2 border-border bg-popover",
          below
            ? "top-0 -translate-y-1 rotate-45 border-l border-t"
            : "top-full -translate-y-1 rotate-45 border-b border-r",
        )}
        style={{ left: arrow, marginLeft: -4 }}
      />
    </div>,
    document.body,
  )
}

export function trendWindowLabel(windowMinutes?: number): string {
  const minutes = windowMinutes && windowMinutes > 0 ? windowMinutes : 60
  const cadence =
    minutes % 60 === 0 ? `每 ${minutes / 60} 分钟` : `每 ${minutes} 秒`
  return `近 ${minutes} 分钟可用率 · ${cadence}测试一次`
}

export function TrendStrip({
  buckets,
  compact = false,
  showAxis = false,
  windowMinutes = 60,
  className,
}: {
  buckets: AvailabilityBucket[]
  compact?: boolean
  showAxis?: boolean
  windowMinutes?: number
  className?: string
}) {
  const list = buckets.length > 0 ? buckets : []
  const count = Math.max(list.length, 60)
  const cells = list.length === 0 ? Array.from({ length: count }) : list
  const [tip, setTip] = useState<TrendTip | null>(null)
  const showTip = (
    event: MouseEvent<HTMLElement>,
    next: { time: string; detail: string },
  ) => {
    const rect = event.currentTarget.getBoundingClientRect()
    setTip({
      ...next,
      rect: {
        left: rect.left,
        top: rect.top,
        width: rect.width,
        height: rect.height,
        bottom: rect.bottom,
      },
    })
  }
  // 详情里空间足够，固定用最宽一档；列表里按容器宽度分档，避免 1fr 把余数像素摊成粗细不齐。
  const barWidth = showAxis
    ? "w-[5px] flex-[0_0_5px]"
    : "w-[3px] flex-[0_0_3px] @min-[320px]:w-[4px] @min-[320px]:flex-[0_0_4px] @min-[420px]:w-[5px] @min-[420px]:flex-[0_0_5px]"
  const barHeight = compact && !showAxis ? "h-[18px]" : "h-6"
  return (
    <div className={cn("@container w-full min-w-0 py-1.5", className)}>
      <div
        className={cn(
          "flex w-max max-w-full items-center",
          showAxis ? "gap-[2px]" : "gap-px @min-[420px]:gap-[2px]",
          compact && !showAxis ? "h-[22px]" : "h-8",
        )}
        aria-label={trendWindowLabel(windowMinutes)}
      >
        {list.length === 0
          ? cells.map((_, idx) => (
              <span
                key={idx}
                className={cn(
                  "relative block origin-center rounded-[1.5px] bg-muted-foreground/20 transition-transform duration-150 hover:z-20 hover:scale-x-[2.4] hover:scale-y-150",
                  barWidth,
                  barHeight,
                )}
                onMouseEnter={(event) => showTip(event, { time: "--:--", detail: "无采样" })}
                onMouseLeave={() => setTip(null)}
              />
            ))
          : list.map((b, idx) => (
              <span
                key={`${b.timestamp}-${idx}`}
                className={cn(
                  "relative block origin-center rounded-[1.5px] transition-transform duration-150 hover:z-20 hover:scale-x-[2.4] hover:scale-y-150",
                  barWidth,
                  bucketBarClass(b.tone, compact && !showAxis),
                )}
                onMouseEnter={(event) => showTip(event, bucketTip(b, windowMinutes))}
                onMouseLeave={() => setTip(null)}
              />
            ))}
      </div>
      {tip ? <TrendHoverCard tip={tip} /> : null}
      {showAxis ? (
        <div className="mt-1 flex w-full max-w-[418px] items-center justify-between font-mono text-[10px] leading-none text-muted-foreground/80">
          <span>{windowMinutes} 分钟前</span>
          <span>{Math.round(windowMinutes / 2)} 分钟前</span>
          <span>现在</span>
        </div>
      ) : null}
    </div>
  )
}

/**
 * 管理台安全 Pelican SVG 沙箱预览组件：
 * 使用携带 Authorization 头的 fetch 获取受鉴权保护的 artifact HTML，
 * 再注入空 sandbox="" 的 iframe srcDoc 中彻底隔离脚本与源。
 */
export function PelicanSandboxFrame({
  runUUID,
  variant = "card",
  className,
}: {
  runUUID: string
  variant?: "thumbnail" | "card"
  className?: string
}) {
  const [doc, setDoc] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let cancelled = false
    setDoc(null)
    setFailed(false)
    const headers: Record<string, string> = {}
    const token = getToken()
    if (token) {
      headers.Authorization = `Bearer ${token}`
    }
    fetch(`/api/intelligence/runs/${encodeURIComponent(runUUID)}/artifact`, {
      headers,
    })
      .then(async (res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        return res.text()
      })
      .then((html) => {
        if (!cancelled) setDoc(html)
      })
      .catch(() => {
        if (!cancelled) setFailed(true)
      })
    return () => {
      cancelled = true
    }
  }, [runUUID])

  if (variant === "thumbnail") {
    return (
      <div
        className={cn(
          "relative h-[30px] w-[42px] shrink-0 overflow-hidden rounded border border-border bg-[#f4f7f4]",
          className,
        )}
      >
        {doc ? (
          <iframe
            sandbox=""
            srcDoc={doc}
            title={`Pelican ${runUUID}`}
            tabIndex={-1}
            className="pointer-events-none absolute top-0 left-0 h-[300px] w-[420px] origin-top-left scale-[0.1] border-0"
          />
        ) : (
          <div className="flex h-full w-full items-center justify-center text-[9px] text-muted-foreground">
            {failed ? "!" : "..."}
          </div>
        )}
      </div>
    )
  }

  return (
    <div
      className={cn(
        "relative aspect-[4/3] w-full overflow-hidden rounded-lg border border-border bg-[#f4f7f4]",
        className,
      )}
    >
      {doc ? (
        <iframe
          sandbox=""
          srcDoc={doc}
          title={`Pelican ${runUUID}`}
          className="block h-full w-full border-0"
        />
      ) : (
        <div className="flex h-full w-full items-center justify-center text-xs text-muted-foreground">
          {failed ? "画面加载失败" : "沙箱画面加载中..."}
        </div>
      )}
    </div>
  )
}
