import { useEffect, useState } from "react"
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
  switch (tone) {
    case "ok":
      return cn(
        compact ? "h-5" : "h-7",
        "rounded-[2.5px] bg-emerald-500/90 dark:bg-emerald-400/90",
      )
    case "warn":
      return cn(
        compact ? "h-5" : "h-7",
        "rounded-[2.5px] bg-amber-500/95 dark:bg-amber-400/95",
      )
    case "down":
      return cn(
        compact ? "h-5" : "h-7",
        "rounded-[2.5px] bg-rose-500/95 dark:bg-rose-400/95",
      )
    default:
      return cn(
        compact ? "h-2.5" : "h-3.5",
        "rounded-[2px] bg-muted-foreground/20",
      )
  }
}

function formatBucketTooltip(b: AvailabilityBucket): string {
  const d = new Date(b.timestamp)
  const hhmm = Number.isNaN(d.getTime())
    ? ""
    : d.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })
  if (b.sample_count <= 0) {
    return `${hhmm} 无采样`
  }
  return `${hhmm} 可用率 ${b.success_rate.toFixed(0)}% (${b.success_count}/${b.sample_count})`
}

export function TrendStrip({
  buckets,
  compact = false,
  showAxis = true,
  className,
}: {
  buckets: AvailabilityBucket[]
  compact?: boolean
  showAxis?: boolean
  className?: string
}) {
  const list = buckets.length > 0 ? buckets : []
  const count = Math.max(list.length, 60)
  return (
    <div className={cn("w-full space-y-1.5", className)}>
      <div
        className={cn(
          "grid w-full items-center gap-[2px] sm:gap-[3px]",
          compact ? "h-6" : "h-8",
        )}
        style={{
          gridTemplateColumns: `repeat(${count}, minmax(2px, 1fr))`,
        }}
        aria-label="近 60 分钟逐分钟可用率"
      >
        {list.length === 0
          ? Array.from({ length: 60 }).map((_, idx) => (
              <span
                key={idx}
                className={cn(
                  "block rounded-[2px] bg-muted-foreground/20",
                  compact ? "h-2.5" : "h-3.5",
                )}
              />
            ))
          : list.map((b, idx) => (
              <span
                key={`${b.timestamp}-${idx}`}
                title={formatBucketTooltip(b)}
                className={cn(
                  "block transition-transform duration-150 hover:scale-y-125",
                  bucketBarClass(b.tone, compact),
                )}
              />
            ))}
      </div>
      {showAxis ? (
        <div className="flex items-center justify-between font-mono text-[10px] leading-none text-muted-foreground/80">
          <span>60 分钟前</span>
          <span className="hidden sm:inline">30 分钟前</span>
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
