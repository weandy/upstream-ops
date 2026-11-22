import { useEffect, useState } from "react"
import { toast } from "sonner"
import { KeyRound, Loader2, RefreshCw, Save, Sparkles } from "lucide-react"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { apiFetch } from "@/lib/api"
import type {
  DetectorConfigInput,
  DetectorMeta,
  DetectorParamField,
  ProbeDetectorConfig,
  ProbeTarget,
} from "@/lib/api-types"
import {
  formatIntervalHuman,
  REASONING_EFFORT_OPTIONS,
} from "@/components/intelligence/shared"
import { cn } from "@/lib/utils"

interface DetectorsDrawerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  target: ProbeTarget | null
  detectors: DetectorMeta[]
  onSaved: () => void
}

interface EditableDetector {
  detector_key: string
  enabled: boolean
  model_name: string
  reasoning_effort: string
  interval_seconds: number
  timeout_seconds: number
  alert_enabled: boolean
  alert_threshold: number
  embed_visible: boolean
  params: Record<string, unknown>
}

function parseParamsObject(raw?: string): Record<string, unknown> {
  if (!raw || !raw.trim()) return {}
  try {
    const v = JSON.parse(raw)
    if (v && typeof v === "object" && !Array.isArray(v)) {
      return v as Record<string, unknown>
    }
  } catch {
    // ignore malformed
  }
  return {}
}

function buildDefaultParams(meta: DetectorMeta): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const f of meta.params_schema ?? []) {
    if (f.default !== undefined) {
      out[f.key] = f.default
    }
  }
  return normalizePelicanParams(meta, out)
}

function normalizeSelectParams(
  meta: DetectorMeta,
  params: Record<string, unknown>,
): Record<string, unknown> {
  const out = { ...params }
  for (const field of meta.params_schema ?? []) {
    if (field.type !== "select" || !field.options?.length) continue
    const current = String(out[field.key] ?? "")
    if (!field.options.includes(current)) {
      out[field.key] = field.default ?? field.options[0]
    }
  }
  return normalizePelicanParams(meta, out)
}

const pelicanBuiltinVersion = "鹈鹕原版html"

interface PelicanVersion {
  name: string
  prompt: string
}

function pelicanVersions(params: Record<string, unknown>): PelicanVersion[] {
  if (!Array.isArray(params.versions)) return []
  return params.versions.flatMap((item) => {
    if (!item || typeof item !== "object") return []
    const row = item as { name?: unknown; prompt?: unknown }
    const name = String(row.name ?? "").trim()
    const prompt = String(row.prompt ?? "")
    return name ? [{ name, prompt }] : []
  })
}

function pelicanBuiltinPrompt(meta: DetectorMeta): string {
  const field = meta.params_schema?.find((item) => item.key === "prompt")
  return String(field?.default ?? "")
}

function normalizePelicanParams(
  meta: DetectorMeta,
  params: Record<string, unknown>,
): Record<string, unknown> {
  if (meta.key !== "pelican") return params
  const versions = pelicanVersions(params)
  const names = new Set(versions.map((item) => item.name))
  let version = String(params.version ?? "").trim()
  if (!version) {
    const legacy = String(params.variant ?? "").trim().toLowerCase()
    version =
      legacy === "original" || legacy === "all-in-one" || legacy === ""
        ? pelicanBuiltinVersion
        : String(params.variant ?? "").trim()
  }
  if (version !== pelicanBuiltinVersion && !names.has(version)) {
    version = pelicanBuiltinVersion
  }
  const builtin = pelicanBuiltinPrompt(meta)
  let prompt = String(params.prompt ?? "")
  if (version !== pelicanBuiltinVersion) {
    prompt = versions.find((item) => item.name === version)?.prompt ?? prompt
  } else if (!prompt.trim()) {
    prompt = builtin
  }
  return { ...params, version, prompt, versions }
}

function serializeParams(
  meta: DetectorMeta,
  params: Record<string, unknown>,
): string {
  const schema = meta.params_schema ?? []
  if (schema.length === 0) return ""
  const out: Record<string, unknown> = {}
  for (const field of schema) {
    const val = params[field.key]
    if (val === undefined || val === null) continue
    if (typeof val === "string" && val.trim() === "" && !field.required) {
      continue
    }
    if (field.type === "number") {
      const n = Number(val)
      if (Number.isFinite(n) && n !== 0) {
        out[field.key] = n
      }
      continue
    }
    out[field.key] = val
  }
  if (meta.key === "pelican") {
    const builtin = pelicanBuiltinPrompt(meta).trim()
    const version = String(params.version ?? pelicanBuiltinVersion).trim() || pelicanBuiltinVersion
    const prompt = String(params.prompt ?? "").trim()
    const versions = pelicanVersions(params).map((item) =>
      item.name === version ? { ...item, prompt } : item,
    )
    out.version = version
    if (version === pelicanBuiltinVersion) {
      if (prompt && prompt !== builtin) out.prompt = prompt
      else delete out.prompt
      if (versions.length > 0) out.versions = versions
    } else {
      delete out.prompt
      out.versions = versions
    }
    delete out.variant
  }
  return Object.keys(out).length > 0 ? JSON.stringify(out) : ""
}

export function DetectorsDrawer({
  open,
  onOpenChange,
  target,
  detectors,
  onSaved,
}: DetectorsDrawerProps) {
  const [rows, setRows] = useState<Record<string, EditableDetector>>({})
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [models, setModels] = useState<string[]>([])
  const [loadingModels, setLoadingModels] = useState(false)
  const [ensuringKey, setEnsuringKey] = useState(false)

  useEffect(() => {
    if (!open || !target) return
    let cancelled = false
    setLoading(true)
    setModels([])
    apiFetch<ProbeDetectorConfig[]>(`/intelligence/targets/${target.id}/detectors`)
      .then((savedList) => {
        if (cancelled) return
        const byKey = new Map<string, ProbeDetectorConfig>()
        for (const item of savedList ?? []) {
          byKey.set(item.detector_key, item)
        }
        const next: Record<string, EditableDetector> = {}
        for (const meta of detectors) {
          const existing = byKey.get(meta.key)
          next[meta.key] = {
            detector_key: meta.key,
            enabled: existing ? existing.enabled : false,
            model_name: existing?.model_name ?? "",
            reasoning_effort: existing?.reasoning_effort || "omit",
            interval_seconds:
              existing?.interval_seconds || meta.default_interval_seconds,
            timeout_seconds:
              existing?.timeout_seconds || meta.default_timeout_seconds,
            alert_enabled: existing ? existing.alert_enabled : true,
            alert_threshold:
              existing?.alert_threshold || meta.default_alert_threshold,
            embed_visible: existing
              ? existing.embed_visible
              : meta.supports_embed,
            params: normalizeSelectParams(meta, {
              ...buildDefaultParams(meta),
              ...parseParamsObject(existing?.params_json),
            }),
          }
        }
        setRows(next)
      })
      .catch((err) => {
        if (!cancelled) {
          toast.error(err instanceof Error ? err.message : "加载插件配置失败")
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [open, target, detectors])

  function updateRow(key: string, patch: Partial<EditableDetector>) {
    setRows((prev) => {
      const cur = prev[key]
      if (!cur) return prev
      return { ...prev, [key]: { ...cur, ...patch } }
    })
  }

  function updateParams(detectorKey: string, patch: Record<string, unknown>) {
    setRows((prev) => {
      const cur = prev[detectorKey]
      if (!cur) return prev
      return {
        ...prev,
        [detectorKey]: {
          ...cur,
          params: { ...cur.params, ...patch },
        },
      }
    })
  }
  function updateParam(detectorKey: string, paramKey: string, value: unknown) {
    updateParams(detectorKey, { [paramKey]: value })
  }

  function applyModelToAll(modelName: string) {
    setRows((prev) => {
      const next: Record<string, EditableDetector> = {}
      for (const [k, v] of Object.entries(prev)) {
        next[k] = { ...v, model_name: modelName }
      }
      return next
    })
    toast.success(`已将全部插件模型设为 ${modelName}`)
  }

  async function handleFetchModels() {
    if (!target) return
    setLoadingModels(true)
    try {
      const list = await apiFetch<string[]>(
        `/intelligence/targets/${target.id}/models`,
      )
      setModels(Array.isArray(list) ? list : [])
      if (list.length === 0) {
        toast.warning("上游返回空模型列表")
      } else {
        toast.success(`成功拉取 ${list.length} 个可用模型`)
      }
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "拉取模型失败")
    } finally {
      setLoadingModels(false)
    }
  }

  async function handleEnsureKey() {
    if (!target) return
    setEnsuringKey(true)
    try {
      const updated = await apiFetch<ProbeTarget>(
        `/intelligence/targets/${target.id}/ensure-key`,
        { method: "POST" },
      )
      toast.success(`已托管 Key：${updated.api_key_hint || updated.api_key_name}`)
      onSaved()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "托管 Key 失败")
    } finally {
      setEnsuringKey(false)
    }
  }

  async function handleSave() {
    if (!target) return
    const items: DetectorConfigInput[] = []
    for (const meta of detectors) {
      const row = rows[meta.key]
      if (!row) continue
      if (row.enabled) {
        if (!row.model_name.trim()) {
          toast.error(`请为已开启的「${meta.name}」填写测试模型 ID`)
          return
        }
        for (const field of meta.params_schema ?? []) {
          if (!field.required) continue
          const val = row.params[field.key]
          if (
            val === undefined ||
            val === null ||
            (typeof val === "string" && val.trim() === "")
          ) {
            toast.error(`请为已开启的「${meta.name}」填写「${field.label}」`)
            return
          }
        }
      }
      items.push({
        detector_key: meta.key,
        enabled: row.enabled,
        model_name: row.model_name.trim(),
        reasoning_effort: meta.supports_reasoning
          ? row.reasoning_effort || "omit"
          : "omit",
        interval_seconds: Math.max(60, Number(row.interval_seconds || meta.default_interval_seconds)),
        timeout_seconds: Math.max(5, Number(row.timeout_seconds || meta.default_timeout_seconds)),
        alert_enabled: row.alert_enabled,
        alert_threshold: Math.max(1, Number(row.alert_threshold || meta.default_alert_threshold)),
        params_json: serializeParams(meta, row.params),
        embed_visible: meta.supports_embed ? row.embed_visible : false,
      })
    }

    setSaving(true)
    try {
      await apiFetch(`/intelligence/targets/${target.id}/detectors`, {
        method: "PUT",
        body: JSON.stringify({ items }),
      })
      toast.success("插拔式检测配置已保存并立即纳入后台调度")
      onSaved()
      onOpenChange(false)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "保存插件配置失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex w-full flex-col gap-0 p-0 sm:max-w-2xl"
      >
        <SheetHeader className="border-b border-border px-5 py-4">
          <SheetTitle>
            插拔式检测配置 · {target?.name ?? ""}
          </SheetTitle>
          <SheetDescription>
            后台内置全部检测类型，可按需勾选开启并分别指定模型、周期与告警阈值。
          </SheetDescription>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={handleFetchModels}
              disabled={loadingModels || !target}
              className="h-8 gap-1.5 text-xs"
            >
              {loadingModels ? (
                <Loader2 className="size-3.5 animate-spin" />
              ) : (
                <RefreshCw className="size-3.5" />
              )}
              拉取上游模型列表
            </Button>
            {target?.source_kind === "channel" &&
            target.api_key_source === "auto_channel_key" ? (
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={handleEnsureKey}
                disabled={ensuringKey}
                className="h-8 gap-1.5 text-xs"
              >
                {ensuringKey ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  <KeyRound className="size-3.5" />
                )}
                托管/刷新渠道 Key
              </Button>
            ) : null}
            {models.length > 0 ? (
              <Select onValueChange={applyModelToAll}>
                <SelectTrigger className="h-8 w-56 text-xs">
                  <SelectValue placeholder="一键批量应用模型到所有插件" />
                </SelectTrigger>
                <SelectContent>
                  {models.map((m) => (
                    <SelectItem key={m} value={m} className="font-mono text-xs">
                      {m}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : null}
          </div>
        </SheetHeader>

        <div className="flex-1 space-y-3 overflow-y-auto px-5 py-4">
          {loading ? (
            <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
              <Loader2 className="mr-2 size-4 animate-spin" />
              加载插件配置中...
            </div>
          ) : (
            detectors.map((meta) => {
              const row = rows[meta.key]
              if (!row) return null
              return (
                <div
                  key={meta.key}
                  className={cn(
                    "rounded-xl border p-4 transition-colors",
                    row.enabled
                      ? "border-foreground/25 bg-card"
                      : "border-border bg-muted/20",
                  )}
                >
                  <div className="flex items-start justify-between gap-3">
                    <div className="space-y-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <h3 className="text-sm font-semibold text-foreground">
                          {meta.name}
                        </h3>
                        <Badge
                          variant="outline"
                          className="font-mono text-[10px]"
                        >
                          {meta.key}
                        </Badge>
                        <Badge
                          variant="secondary"
                          className="text-[10px] font-normal"
                        >
                          {meta.category === "liveness" ? "主动测活" : "智力监测"}
                        </Badge>
                      </div>
                      <p className="text-xs leading-5 text-muted-foreground">
                        {meta.description}
                      </p>
                    </div>
                    <Switch
                      checked={row.enabled}
                      onCheckedChange={(v) => updateRow(meta.key, { enabled: v })}
                      aria-label={`开关 ${meta.name}`}
                    />
                  </div>

                  <div className="mt-3.5 grid gap-3 sm:grid-cols-2">
                    <div className="space-y-1.5">
                      <Label className="text-xs">测试模型 ID</Label>
                      <div className="flex gap-1.5">
                        <Input
                          value={row.model_name}
                          placeholder="例如：gpt-6.2-sol / claude-opus-4-6"
                          className="h-8 font-mono text-xs"
                          onChange={(e) =>
                            updateRow(meta.key, { model_name: e.target.value })
                          }
                        />
                        {models.length > 0 ? (
                          <Select
                            value={
                              models.includes(row.model_name)
                                ? row.model_name
                                : ""
                            }
                            onValueChange={(v) =>
                              updateRow(meta.key, { model_name: v })
                            }
                          >
                            <SelectTrigger className="h-8 w-24 shrink-0 text-xs">
                              <Sparkles className="size-3" />
                              <SelectValue placeholder="选用" />
                            </SelectTrigger>
                            <SelectContent>
                              {models.map((m) => (
                                <SelectItem
                                  key={m}
                                  value={m}
                                  className="font-mono text-xs"
                                >
                                  {m}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        ) : null}
                      </div>
                    </div>

                    {meta.supports_reasoning ? (
                      <div className="space-y-1.5">
                        <Label className="text-xs">推理强度 reasoning_effort</Label>
                        <Select
                          value={row.reasoning_effort || "omit"}
                          onValueChange={(v) =>
                            updateRow(meta.key, { reasoning_effort: v })
                          }
                        >
                          <SelectTrigger className="h-8 w-full text-xs">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {REASONING_EFFORT_OPTIONS.map((opt) => (
                              <SelectItem key={opt.value} value={opt.value}>
                                {opt.label}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </div>
                    ) : null}

                    <div className="space-y-1.5">
                      <Label className="text-xs">
                        执行周期（秒 · 当前{" "}
                        {formatIntervalHuman(row.interval_seconds)}）
                      </Label>
                      <Input
                        type="number"
                        min={60}
                        className="h-8 font-mono text-xs"
                        value={String(row.interval_seconds)}
                        onChange={(e) =>
                          updateRow(meta.key, {
                            interval_seconds: Number(e.target.value || 60),
                          })
                        }
                      />
                    </div>

                    <div className="space-y-1.5">
                      <Label className="text-xs">单次超时（秒）</Label>
                      <Input
                        type="number"
                        min={5}
                        className="h-8 font-mono text-xs"
                        value={String(row.timeout_seconds)}
                        onChange={(e) =>
                          updateRow(meta.key, {
                            timeout_seconds: Number(e.target.value || 30),
                          })
                        }
                      />
                    </div>
                  </div>

                  <div className="mt-3 flex flex-wrap items-center gap-4 border-t border-border/60 pt-3">
                    <label className="flex items-center gap-2 text-xs">
                      <Switch
                        checked={row.alert_enabled}
                        onCheckedChange={(v) =>
                          updateRow(meta.key, { alert_enabled: v })
                        }
                      />
                      <span>异常推送告警</span>
                    </label>
                    <div className="flex items-center gap-1.5 text-xs">
                      <span className="text-muted-foreground">连续异常</span>
                      <Input
                        type="number"
                        min={1}
                        max={20}
                        className="h-7 w-16 font-mono text-xs"
                        value={String(row.alert_threshold)}
                        onChange={(e) =>
                          updateRow(meta.key, {
                            alert_threshold: Number(e.target.value || 1),
                          })
                        }
                      />
                      <span className="text-muted-foreground">次触发</span>
                    </div>
                    {meta.supports_embed ? (
                      <label className="ml-auto flex items-center gap-2 text-xs">
                        <Switch
                          checked={row.embed_visible}
                          onCheckedChange={(v) =>
                            updateRow(meta.key, { embed_visible: v })
                          }
                        />
                        <span>对外 Embed 可见</span>
                      </label>
                    ) : null}
                  </div>

                  {meta.params_schema && meta.params_schema.length > 0 ? (
                    <div className="mt-3 space-y-2.5 rounded-lg border border-border/70 bg-muted/30 p-3">
                      <p className="text-[11px] font-medium text-muted-foreground">
                        插件专属参数
                      </p>
                      <div className="grid gap-2.5 sm:grid-cols-2">
                        {meta.key === "pelican" ? (
                          <PelicanVersionControls
                            params={row.params}
                            builtinPrompt={pelicanBuiltinPrompt(meta)}
                            onChange={(patch) => updateParams(meta.key, patch)}
                          />
                        ) : null}
                        {meta.params_schema.map((field) => (
                          <DynamicParamControl
                            key={field.key}
                            field={field}
                            value={row.params[field.key]}
                            onChange={(val) =>
                              updateParam(meta.key, field.key, val)
                            }
                          />
                        ))}
                      </div>
                    </div>
                  ) : null}
                </div>
              )
            })
          )}
        </div>

        <SheetFooter className="flex-row items-center justify-end gap-2 border-t border-border px-5 py-3">
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={saving}
          >
            取消
          </Button>
          <Button
            type="button"
            onClick={handleSave}
            disabled={saving || loading}
            className="gap-1.5"
          >
            {saving ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : (
              <Save className="size-3.5" />
            )}
            {saving ? "保存中..." : "保存配置"}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function DynamicParamControl({
  field,
  value,
  onChange,
}: {
  field: DetectorParamField
  value: unknown
  onChange: (val: unknown) => void
}) {
  const fullWidth = field.type === "textarea"
  return (
    <div className={cn("space-y-1", fullWidth && "sm:col-span-2")}>
      <Label className="text-xs">
        {field.label}
        {field.required ? <span className="ml-0.5 text-destructive">*</span> : null}
      </Label>
      {field.type === "textarea" ? (
        <Textarea
          rows={3}
          className="text-xs"
          value={String(value ?? "")}
          placeholder={field.description}
          onChange={(e) => onChange(e.target.value)}
        />
      ) : field.type === "select" ? (
        <Select
          value={String(value ?? field.default ?? "")}
          onValueChange={(v) => onChange(v)}
        >
          <SelectTrigger className="h-8 w-full text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(field.options ?? []).map((opt) => (
              <SelectItem key={opt} value={opt}>
                {opt}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : field.type === "boolean" ? (
        <div className="flex h-8 items-center">
          <Switch
            checked={Boolean(value ?? field.default ?? false)}
            onCheckedChange={(v) => onChange(v)}
          />
        </div>
      ) : field.type === "number" ? (
        <Input
          type="number"
          className="h-8 font-mono text-xs"
          value={value === undefined || value === null ? "" : String(value)}
          placeholder={
            field.default !== undefined ? String(field.default) : undefined
          }
          onChange={(e) =>
            onChange(e.target.value === "" ? "" : Number(e.target.value))
          }
        />
      ) : (
        <Input
          className="h-8 text-xs"
          value={String(value ?? "")}
          placeholder={field.description}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
      {field.description ? (
        <p className="text-[11px] leading-4 text-muted-foreground">
          {field.description}
        </p>
      ) : null}
    </div>
  )
}

function PelicanVersionControls({
  params,
  builtinPrompt,
  onChange,
}: {
  params: Record<string, unknown>
  builtinPrompt: string
  onChange: (patch: Record<string, unknown>) => void
}) {
  const [naming, setNaming] = useState(false)
  const [draftName, setDraftName] = useState("")
  const versions = pelicanVersions(params)
  const version = String(params.version ?? pelicanBuiltinVersion)
  const prompt = String(params.prompt ?? "")
  const custom = version !== pelicanBuiltinVersion
  const dirtyBuiltin = !custom && prompt.trim() !== builtinPrompt.trim()

  function rememberCurrent(nextVersions = versions) {
    if (!custom) return nextVersions
    return nextVersions.map((item) =>
      item.name === version ? { ...item, prompt } : item,
    )
  }

  function selectVersion(next: string) {
    const stored = rememberCurrent()
    if (next === pelicanBuiltinVersion) {
      const restored = String(params.builtin_prompt ?? "").trim()
        ? String(params.builtin_prompt)
        : builtinPrompt
      onChange({
        version: next,
        versions: stored,
        prompt: restored,
        builtin_prompt: "",
      })
      return
    }
    onChange({
      version: next,
      versions: stored,
      prompt: stored.find((item) => item.name === next)?.prompt ?? "",
      builtin_prompt:
        !custom && prompt !== builtinPrompt ? prompt : params.builtin_prompt ?? "",
    })
  }

  function saveAs() {
    const name = draftName.trim()
    if (!name) return
    if (name === pelicanBuiltinVersion || versions.some((item) => item.name === name)) {
      toast.error("版本名称已存在")
      return
    }
    if (!prompt.trim()) {
      toast.error("题目不能为空")
      return
    }
    onChange({
      version: name,
      versions: [...rememberCurrent(), { name, prompt: prompt.trim() }],
      prompt,
    })
    setDraftName("")
    setNaming(false)
  }

  function removeVersion() {
    if (!custom) return
    onChange({
      version: pelicanBuiltinVersion,
      versions: versions.filter((item) => item.name !== version),
      prompt: builtinPrompt,
    })
  }

  return (
    <div className="space-y-1 sm:col-span-2">
      <div className="flex items-center gap-1.5">
        <Label className="shrink-0 text-xs">题型版本</Label>
        <Select value={version} onValueChange={selectVersion}>
          <SelectTrigger className="h-8 min-w-0 flex-1 text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={pelicanBuiltinVersion}>{pelicanBuiltinVersion}</SelectItem>
            {versions.map((item) => (
              <SelectItem key={item.name} value={item.name}>
                {item.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {dirtyBuiltin ? (
          <Button
            type="button"
            variant="ghost"
            className="h-8 shrink-0 px-2 text-xs"
            onClick={() => onChange({ prompt: builtinPrompt })}
          >
            恢复默认
          </Button>
        ) : null}
        {custom ? (
          <Button
            type="button"
            variant="ghost"
            className="h-8 shrink-0 px-2 text-xs"
            onClick={removeVersion}
          >
            删除
          </Button>
        ) : null}
        <Button
          type="button"
          variant="ghost"
          className="h-8 shrink-0 px-2 text-xs"
          onClick={() => setNaming((open) => !open)}
        >
          另存
        </Button>
      </div>
      {naming ? (
        <div className="flex items-center gap-1.5">
          <Input
            className="h-8 text-xs"
            value={draftName}
            placeholder="新版本名称"
            onChange={(e) => setDraftName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") saveAs()
            }}
          />
          <Button type="button" variant="outline" className="h-8 px-2 text-xs" onClick={saveAs}>
            确认
          </Button>
        </div>
      ) : null}
    </div>
  )
}
