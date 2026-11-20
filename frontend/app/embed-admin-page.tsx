import { useEffect, useMemo, useState, type FormEvent } from "react"
import { toast } from "sonner"
import {
  ArrowDown,
  ArrowUp,
  Check,
  Code2,
  Copy,
  ExternalLink,
  Loader2,
  Monitor,
  PanelTop,
  Plus,
  RefreshCw,
  Save,
  Shield,
  Smartphone,
  Tablet,
  Trash2,
} from "lucide-react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useConfirm } from "@/components/ui/confirm-dialog"
import { apiFetch } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { useEmbedSites, useIntelligenceTargets } from "@/lib/queries"
import type {
  EmbedSite,
  EmbedSiteInput,
  EmbedTheme,
  ProbeTarget,
} from "@/lib/api-types"
import { FAMILY_META } from "@/components/intelligence/shared"
import { cn } from "@/lib/utils"

interface SiteFormState {
  id: number | null
  slug: string
  title: string
  eyebrow: string
  enabled: boolean
  refresh_seconds: string
  allowed_origins_text: string
  cors_origins_text: string
  theme: EmbedTheme
  target_ids: number[]
  show_identity_column: boolean
}

function blankSiteForm(): SiteFormState {
  return {
    id: null,
    slug: "",
    title: "渠道性能",
    eyebrow: "CHANNEL PERFORMANCE",
    enabled: true,
    refresh_seconds: "15",
    allowed_origins_text: "",
    cors_origins_text: "",
    theme: "paper",
    target_ids: [],
    show_identity_column: true,
  }
}

function siteToForm(site: EmbedSite): SiteFormState {
  return {
    id: site.id,
    slug: site.slug,
    title: site.title,
    eyebrow: site.eyebrow,
    enabled: site.enabled,
    refresh_seconds: String(site.refresh_seconds ?? 15),
    allowed_origins_text: (site.allowed_origins ?? []).join("\n"),
    cors_origins_text: (site.cors_origins ?? []).join("\n"),
    theme: site.theme || "paper",
    target_ids: [...(site.target_ids ?? [])],
    show_identity_column: site.show_identity_column,
  }
}

function splitOrigins(text: string): string[] {
  return text
    .split(/[\r\n,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

const THEME_LABELS: Record<EmbedTheme, string> = {
  paper: "暖纸杂志风 (paper)",
  light: "清爽浅色 (light)",
  dark: "深色观测台 (dark)",
}

type ViewportMode = "desktop" | "tablet" | "mobile"

export default function EmbedAdminPage() {
  const sitesQuery = useEmbedSites()
  const targetsQuery = useIntelligenceTargets()
  const { confirm, dialog: confirmDialog } = useConfirm()

  const sites = useMemo(() => sitesQuery.data ?? [], [sitesQuery.data])
  const targets = useMemo(() => targetsQuery.data ?? [], [targetsQuery.data])

  const [selectedID, setSelectedID] = useState<number | "new" | null>(null)
  const [form, setForm] = useState<SiteFormState>(() => blankSiteForm())
  const [saving, setSaving] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [viewport, setViewport] = useState<ViewportMode>("desktop")
  const [previewKey, setPreviewKey] = useState(1)
  const [copiedKey, setCopiedKey] = useState<string | null>(null)

  // 初次加载站点后默认选中第一个站点；若列表为空保持新建默认表单。
  useEffect(() => {
    if (selectedID !== null) return
    if (sites.length > 0) {
      setSelectedID(sites[0].id)
      setForm(siteToForm(sites[0]))
    }
  }, [sites, selectedID])

  const activeSite = useMemo(() => {
    if (typeof selectedID !== "number") return null
    return sites.find((s) => s.id === selectedID) ?? null
  }, [sites, selectedID])

  const targetByID = useMemo(() => {
    const map = new Map<number, ProbeTarget>()
    for (const t of targets) map.set(t.id, t)
    return map
  }, [targets])

  function selectSite(site: EmbedSite) {
    setSelectedID(site.id)
    setForm(siteToForm(site))
    setPreviewKey((k) => k + 1)
  }

  function startNewSite() {
    setSelectedID("new")
    setForm(blankSiteForm())
  }

  function toggleTargetID(id: number, checked: boolean) {
    setForm((prev) => {
      if (checked) {
        if (prev.target_ids.includes(id)) return prev
        return { ...prev, target_ids: [...prev.target_ids, id] }
      }
      return { ...prev, target_ids: prev.target_ids.filter((x) => x !== id) }
    })
  }

  function moveTargetID(idx: number, delta: number) {
    setForm((prev) => {
      const next = prev.target_ids.slice()
      const targetIdx = idx + delta
      if (targetIdx < 0 || targetIdx >= next.length) return prev
      const [item] = next.splice(idx, 1)
      next.splice(targetIdx, 0, item)
      return { ...prev, target_ids: next }
    })
  }

  async function handleSave(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (!form.slug.trim()) {
      toast.error("请填写站点 Slug，例如 default 或 ooioo")
      return
    }
    const payload: EmbedSiteInput = {
      slug: form.slug.trim(),
      title: form.title.trim() || "渠道性能",
      eyebrow: form.eyebrow.trim() || "CHANNEL PERFORMANCE",
      enabled: form.enabled,
      refresh_seconds: Math.max(0, Number(form.refresh_seconds || 0)),
      allowed_origins: splitOrigins(form.allowed_origins_text),
      cors_origins: splitOrigins(form.cors_origins_text),
      theme: form.theme,
      target_ids: form.target_ids,
      show_identity_column: form.show_identity_column,
    }

    setSaving(true)
    try {
      const saved =
        form.id != null
          ? await apiFetch<EmbedSite>(`/embed-sites/${form.id}`, {
              method: "PUT",
              body: JSON.stringify(payload),
            })
          : await apiFetch<EmbedSite>("/embed-sites", {
              method: "POST",
              body: JSON.stringify(payload),
            })
      toast.success(
        form.id != null
          ? `已更新嵌入站点 ${saved.slug}`
          : `已创建嵌入站点 ${saved.slug}`,
      )
      setSelectedID(saved.id)
      setForm(siteToForm(saved))
      setPreviewKey((k) => k + 1)
      sitesQuery.refetch()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "保存站点配置失败")
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete() {
    if (!activeSite) return
    const ok = await confirm({
      title: `删除嵌入站点 ${activeSite.slug}？`,
      description:
        "删除后外部引用 /embed?site=" + activeSite.slug + " 将立即失效（返回 404）。",
      confirmLabel: "删除",
      destructive: true,
    })
    if (!ok) return
    setDeleting(true)
    try {
      await apiFetch(`/embed-sites/${activeSite.id}`, { method: "DELETE" })
      toast.success(`已删除嵌入站点 ${activeSite.slug}`)
      setSelectedID("new")
      setForm(blankSiteForm())
      sitesQuery.refetch()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "删除失败")
    } finally {
      setDeleting(false)
    }
  }

  async function handleCopy(key: string, text: string) {
    try {
      await copyText(text)
      setCopiedKey(key)
      toast.success("已复制到剪贴板")
      setTimeout(() => {
        setCopiedKey((cur) => (cur === key ? null : cur))
      }, 1500)
    } catch {
      toast.error("复制失败，请手动选中复制")
    }
  }

  const origin =
    typeof window !== "undefined" ? window.location.origin : "http://localhost:8418"
  const effectiveSlug = activeSite?.slug || (sites.length === 0 ? "default" : form.slug.trim() || "default")
  const previewPath = `/embed?site=${encodeURIComponent(effectiveSlug)}`
  const fullEmbedURL = `${origin}${previewPath}`
  const fullPublicAPIURL = `${origin}/api/public/status?site=${encodeURIComponent(effectiveSlug)}`
  const iframeSnippet = `<iframe\n  src="${fullEmbedURL}"\n  title="${form.title || "渠道性能"}"\n  sandbox="allow-same-origin"\n  loading="lazy"\n  referrerpolicy="no-referrer"\n  style="width:100%;min-height:560px;border:0;border-radius:12px;overflow:hidden"\n></iframe>`

  return (
    <section className="space-y-5">
      <header className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div className="space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-lg font-semibold tracking-tight text-foreground sm:text-xl">
              外部嵌入组件管理
            </h1>
            <Badge
              variant="outline"
              className="border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300"
            >
              零 JS SSR · 动态 CSP 白名单
            </Badge>
          </div>
          <p className="max-w-3xl text-xs leading-5 text-muted-foreground sm:text-sm">
            配置一个或多个对外公开的性能看板实例（/embed?site=xxx），通过动态 frame-ancestors 白名单防盗链，外部站点粘贴一行 iframe 即可安全嵌入。
          </p>
        </div>

        <Button size="sm" onClick={startNewSite} className="w-fit gap-1.5">
          <Plus className="size-3.5" />
          新建嵌入站点
        </Button>
      </header>

      {/* 站点快速切换条 */}
      <div className="flex flex-wrap items-center gap-2">
        {sitesQuery.loading && sites.length === 0 ? (
          <span className="text-xs text-muted-foreground">加载站点列表...</span>
        ) : sites.length === 0 ? (
          <div className="rounded-lg border border-dashed border-border px-3 py-2 text-xs text-muted-foreground">
            尚未创建自定义嵌入站点（当前 /embed?site=default 使用仅限同源的默认只读视图，保存下方表单即可持久化配置并添加外部白名单域名）。
          </div>
        ) : (
          sites.map((site) => {
            const active = selectedID === site.id
            return (
              <button
                key={site.id}
                type="button"
                onClick={() => selectSite(site)}
                className={cn(
                  "inline-flex cursor-pointer items-center gap-2 rounded-lg border px-3 py-1.5 text-xs transition-colors",
                  active
                    ? "border-foreground/30 bg-muted font-medium text-foreground"
                    : "border-border bg-card text-muted-foreground hover:bg-muted/50 hover:text-foreground",
                )}
              >
                <span
                  className={cn(
                    "size-2 rounded-full",
                    site.enabled ? "bg-emerald-500" : "bg-muted-foreground/40",
                  )}
                />
                <span className="font-mono">{site.slug}</span>
                <span className="text-[11px] opacity-75">· {site.title}</span>
              </button>
            )
          })
        )}
      </div>

      {/* 双栏工作台 */}
      <div className="grid gap-5 xl:grid-cols-[minmax(380px,460px)_1fr]">
        {/* 左侧：配置表单 */}
        <Card className="border-border shadow-none">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 border-b border-border px-5 py-3.5">
            <CardTitle className="flex items-center gap-2 text-sm font-semibold">
              <PanelTop className="size-4 text-sky-600 dark:text-sky-400" />
              {form.id != null ? `编辑站点 · ${form.slug}` : "新建嵌入站点"}
            </CardTitle>
            {activeSite ? (
              <Button
                type="button"
                size="sm"
                variant="ghost"
                onClick={handleDelete}
                disabled={deleting}
                className="h-7 gap-1 text-xs text-muted-foreground hover:text-destructive"
              >
                <Trash2 className="size-3.5" />
                删除
              </Button>
            ) : null}
          </CardHeader>
          <CardContent className="p-5">
            <form onSubmit={handleSave} className="space-y-4">
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="es-slug" className="text-xs">
                    站点标识 Slug
                  </Label>
                  <Input
                    id="es-slug"
                    className="h-8 font-mono text-xs"
                    placeholder="default 或 ooioo"
                    value={form.slug}
                    onChange={(e) => setForm({ ...form, slug: e.target.value })}
                    required
                    disabled={saving}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="es-theme" className="text-xs">
                    外观主题
                  </Label>
                  <Select
                    value={form.theme}
                    onValueChange={(v) =>
                      setForm({ ...form, theme: v as EmbedTheme })
                    }
                    disabled={saving}
                  >
                    <SelectTrigger id="es-theme" className="h-8 w-full text-xs">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {(Object.keys(THEME_LABELS) as EmbedTheme[]).map((k) => (
                        <SelectItem key={k} value={k}>
                          {THEME_LABELS[k]}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="es-title" className="text-xs">
                    页面主标题
                  </Label>
                  <Input
                    id="es-title"
                    className="h-8 text-xs"
                    value={form.title}
                    onChange={(e) =>
                      setForm({ ...form, title: e.target.value })
                    }
                    disabled={saving}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="es-eyebrow" className="text-xs">
                    顶标文字 Eyebrow
                  </Label>
                  <Input
                    id="es-eyebrow"
                    className="h-8 font-mono text-xs"
                    value={form.eyebrow}
                    onChange={(e) =>
                      setForm({ ...form, eyebrow: e.target.value })
                    }
                    disabled={saving}
                  />
                </div>
              </div>

              <div className="grid gap-3 sm:grid-cols-3">
                <div className="space-y-1.5">
                  <Label htmlFor="es-refresh" className="text-xs">
                    自动刷新（秒）
                  </Label>
                  <Input
                    id="es-refresh"
                    type="number"
                    min={0}
                    max={3600}
                    className="h-8 font-mono text-xs"
                    value={form.refresh_seconds}
                    onChange={(e) =>
                      setForm({ ...form, refresh_seconds: e.target.value })
                    }
                    disabled={saving}
                  />
                </div>
                <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2">
                  <span className="text-xs font-medium">启用站点</span>
                  <Switch
                    checked={form.enabled}
                    onCheckedChange={(v) => setForm({ ...form, enabled: v })}
                    disabled={saving}
                  />
                </div>
                <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2">
                  <span className="text-xs font-medium">模型检测列</span>
                  <Switch
                    checked={form.show_identity_column}
                    onCheckedChange={(v) =>
                      setForm({ ...form, show_identity_column: v })
                    }
                    disabled={saving}
                  />
                </div>
              </div>

              <div className="space-y-2.5 rounded-lg border border-border p-3">
                <div className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                  <Shield className="size-3.5 text-emerald-600 dark:text-emerald-400" />
                  安全防盗链与跨域白名单
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="es-allowed" className="text-xs">
                    允许嵌入父站点 Origin（CSP frame-ancestors，每行一个）
                  </Label>
                  <Textarea
                    id="es-allowed"
                    rows={3}
                    className="font-mono text-xs"
                    placeholder={"https://ooioo.work\nhttps://*.example.com"}
                    value={form.allowed_origins_text}
                    onChange={(e) =>
                      setForm({ ...form, allowed_origins_text: e.target.value })
                    }
                    disabled={saving}
                  />
                  <p className="text-[11px] text-muted-foreground">
                    始终默认允许同源 (&apos;self&apos;)；留空则仅允许管理台自身预览。
                  </p>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="es-cors" className="text-xs">
                    允许跨域读取 /api/public/status 的 Origin（每行一个）
                  </Label>
                  <Textarea
                    id="es-cors"
                    rows={2}
                    className="font-mono text-xs"
                    placeholder={"https://ooioo.work"}
                    value={form.cors_origins_text}
                    onChange={(e) =>
                      setForm({ ...form, cors_origins_text: e.target.value })
                    }
                    disabled={saving}
                  />
                </div>
              </div>

              <div className="space-y-2.5 rounded-lg border border-border p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-medium text-foreground">
                    展示的监测目标
                  </span>
                  <span className="text-[11px] text-muted-foreground">
                    {form.target_ids.length === 0
                      ? "全部已开启 Embed 的目标"
                      : `已选 ${form.target_ids.length} 个（按下方顺序）`}
                  </span>
                </div>

                {targets.length === 0 ? (
                  <p className="text-xs text-muted-foreground">
                    暂无监测目标，请先到「智力监测」页面创建目标并勾选「对外 Embed」。
                  </p>
                ) : (
                  <div className="max-h-48 space-y-1.5 overflow-y-auto pr-1">
                    {targets.map((t) => {
                      const checked = form.target_ids.includes(t.id)
                      const fam = FAMILY_META[t.family] ?? FAMILY_META.other
                      return (
                        <label
                          key={t.id}
                          className="flex cursor-pointer items-center justify-between gap-2 rounded-md border border-border/60 px-2.5 py-1.5 text-xs hover:bg-muted/40"
                        >
                          <div className="flex min-w-0 items-center gap-2">
                            <Checkbox
                              checked={checked}
                              onCheckedChange={(v) =>
                                toggleTargetID(t.id, Boolean(v))
                              }
                            />
                            <Badge
                              variant="outline"
                              className={cn("text-[10px]", fam.badgeClass)}
                            >
                              {fam.label}
                            </Badge>
                            <span className="truncate font-medium">
                              {t.name}
                            </span>
                          </div>
                          {!t.embed_visible || !t.enabled ? (
                            <Badge
                              variant="secondary"
                              className="shrink-0 text-[10px]"
                            >
                              {!t.enabled ? "已暂停" : "未开 Embed"}
                            </Badge>
                          ) : null}
                        </label>
                      )
                    })}
                  </div>
                )}

                {form.target_ids.length > 1 ? (
                  <div className="space-y-1 border-t border-border/60 pt-2">
                    <p className="text-[11px] text-muted-foreground">
                      自定义展示顺序
                    </p>
                    {form.target_ids.map((id, idx) => {
                      const t = targetByID.get(id)
                      return (
                        <div
                          key={id}
                          className="flex items-center justify-between rounded bg-muted/40 px-2 py-1 text-xs"
                        >
                          <span className="truncate font-mono">
                            {idx + 1}. {t?.name ?? `Target #${id}`}
                          </span>
                          <div className="flex items-center gap-1">
                            <Button
                              type="button"
                              size="icon"
                              variant="ghost"
                              className="size-6"
                              disabled={idx === 0}
                              onClick={() => moveTargetID(idx, -1)}
                            >
                              <ArrowUp className="size-3" />
                            </Button>
                            <Button
                              type="button"
                              size="icon"
                              variant="ghost"
                              className="size-6"
                              disabled={idx === form.target_ids.length - 1}
                              onClick={() => moveTargetID(idx, 1)}
                            >
                              <ArrowDown className="size-3" />
                            </Button>
                          </div>
                        </div>
                      )
                    })}
                  </div>
                ) : null}
              </div>

              <Button
                type="submit"
                disabled={saving}
                className="w-full gap-1.5"
              >
                {saving ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  <Save className="size-3.5" />
                )}
                {saving
                  ? "保存中..."
                  : form.id != null
                    ? "保存站点配置并刷新预览"
                    : "创建嵌入站点"}
              </Button>
            </form>
          </CardContent>
        </Card>

        {/* 右侧：实时 iframe 预览与一键复制代码 */}
        <div className="space-y-4">
          <Card className="overflow-hidden border-border shadow-none">
            <CardHeader className="flex flex-col gap-2 border-b border-border px-5 py-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="space-y-0.5">
                <CardTitle className="text-sm font-semibold">
                  实时零 JS 嵌入预览
                </CardTitle>
                <p className="font-mono text-[11px] text-muted-foreground">
                  {previewPath}
                </p>
              </div>
              <div className="flex flex-wrap items-center gap-1.5">
                <div className="inline-flex rounded-md border border-border bg-muted/30 p-0.5">
                  <Button
                    type="button"
                    size="icon"
                    variant={viewport === "desktop" ? "secondary" : "ghost"}
                    className="size-7"
                    title="桌面宽屏"
                    onClick={() => setViewport("desktop")}
                  >
                    <Monitor className="size-3.5" />
                  </Button>
                  <Button
                    type="button"
                    size="icon"
                    variant={viewport === "tablet" ? "secondary" : "ghost"}
                    className="size-7"
                    title="平板视口"
                    onClick={() => setViewport("tablet")}
                  >
                    <Tablet className="size-3.5" />
                  </Button>
                  <Button
                    type="button"
                    size="icon"
                    variant={viewport === "mobile" ? "secondary" : "ghost"}
                    className="size-7"
                    title="窄屏视口"
                    onClick={() => setViewport("mobile")}
                  >
                    <Smartphone className="size-3.5" />
                  </Button>
                </div>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  className="h-8 gap-1 text-xs"
                  onClick={() => setPreviewKey((k) => k + 1)}
                >
                  <RefreshCw className="size-3.5" />
                  刷新
                </Button>
                <Button
                  asChild
                  size="sm"
                  variant="outline"
                  className="h-8 gap-1 text-xs"
                >
                  <a
                    href={previewPath}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    <ExternalLink className="size-3.5" />
                    新窗打开
                  </a>
                </Button>
              </div>
            </CardHeader>
            <CardContent className="bg-muted/25 p-4">
              {selectedID === "new" && sites.length > 0 ? (
                <div className="flex h-[460px] items-center justify-center rounded-lg border border-dashed border-border bg-background text-xs text-muted-foreground">
                  保存新站点后即可在此实时预览零 JS 服务端渲染效果。
                </div>
              ) : (
                <div className="flex justify-center">
                  <iframe
                    key={`${effectiveSlug}-${previewKey}`}
                    src={previewPath}
                    title={`Embed Preview ${effectiveSlug}`}
                    sandbox="allow-same-origin"
                    className={cn(
                      "h-[480px] rounded-xl border border-border bg-background shadow-xs transition-all",
                      viewport === "desktop" && "w-full",
                      viewport === "tablet" && "w-full max-w-[760px]",
                      viewport === "mobile" && "w-full max-w-[430px]",
                    )}
                  />
                </div>
              )}
            </CardContent>
          </Card>

          <Card className="border-border shadow-none">
            <CardHeader className="border-b border-border px-5 py-3">
              <CardTitle className="flex items-center gap-2 text-sm font-semibold">
                <Code2 className="size-4 text-emerald-600 dark:text-emerald-400" />
                外部站点一键接入代码
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-3.5 p-5 text-xs">
              <div className="space-y-1.5">
                <div className="flex items-center justify-between">
                  <Label className="text-xs">
                    HTML iframe 嵌入代码（直接粘贴到 NewAPI / Sub2API 首页或官网）
                  </Label>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    className="h-7 gap-1 text-xs"
                    onClick={() => handleCopy("iframe", iframeSnippet)}
                  >
                    {copiedKey === "iframe" ? (
                      <Check className="size-3.5 text-emerald-600" />
                    ) : (
                      <Copy className="size-3.5" />
                    )}
                    复制代码
                  </Button>
                </div>
                <pre className="overflow-x-auto rounded-lg border border-border bg-muted/40 p-3 font-mono text-[11px] leading-5">
                  {iframeSnippet}
                </pre>
              </div>

              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1">
                  <Label className="text-xs">公开嵌入直链</Label>
                  <div className="flex gap-1.5">
                    <Input
                      readOnly
                      value={fullEmbedURL}
                      className="h-8 font-mono text-[11px]"
                    />
                    <Button
                      type="button"
                      size="icon"
                      variant="outline"
                      className="size-8 shrink-0"
                      onClick={() => handleCopy("url", fullEmbedURL)}
                    >
                      {copiedKey === "url" ? (
                        <Check className="size-3.5 text-emerald-600" />
                      ) : (
                        <Copy className="size-3.5" />
                      )}
                    </Button>
                  </div>
                </div>
                <div className="space-y-1">
                  <Label className="text-xs">公开只读 JSON API</Label>
                  <div className="flex gap-1.5">
                    <Input
                      readOnly
                      value={fullPublicAPIURL}
                      className="h-8 font-mono text-[11px]"
                    />
                    <Button
                      type="button"
                      size="icon"
                      variant="outline"
                      className="size-8 shrink-0"
                      onClick={() => handleCopy("api", fullPublicAPIURL)}
                    >
                      {copiedKey === "api" ? (
                        <Check className="size-3.5 text-emerald-600" />
                      ) : (
                        <Copy className="size-3.5" />
                      )}
                    </Button>
                  </div>
                </div>
              </div>
            </CardContent>
          </Card>
        </div>
      </div>

      {confirmDialog}
    </section>
  )
}
