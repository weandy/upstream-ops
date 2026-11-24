import { useEffect, useState, type FormEvent } from "react"
import { toast } from "sonner"
import { KeyRound, Loader2 } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
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
import { useChannels } from "@/lib/queries"
import type {
  ChannelAPIKeyGroup,
  ProbeAPIKeySource,
  ProbeAuthStyle,
  ProbeFamily,
  ProbeProtocol,
  ProbeSourceKind,
  ProbeTarget,
  ProbeTargetInput,
} from "@/lib/api-types"
import { FAMILY_META, PROTOCOL_LABELS } from "@/components/intelligence/shared"

interface TargetFormDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  target?: ProbeTarget | null
  onSaved: (target: ProbeTarget) => void
}

interface FormState {
  name: string
  slug: string
  family: ProbeFamily
  source_kind: ProbeSourceKind
  channel_id: string
  source_group_id: string
  source_group_name: string
  base_url: string
  api_key_source: ProbeAPIKeySource
  api_key: string
  protocol: ProbeProtocol
  auth_style: ProbeAuthStyle
  proxy_enabled: boolean
  user_agent: string
  extra_headers_json: string
  sort_order: string
  enabled: boolean
  embed_visible: boolean
  notes: string
}

function initialForm(t?: ProbeTarget | null): FormState {
  if (!t) {
    return {
      name: "",
      slug: "",
      family: "other",
      source_kind: "direct",
      channel_id: "",
      source_group_id: "",
      source_group_name: "",
      base_url: "",
      api_key_source: "manual",
      api_key: "",
      protocol: "openai_chat",
      auth_style: "bearer",
      proxy_enabled: false,
      user_agent: "",
      extra_headers_json: "",
      sort_order: "0",
      enabled: true,
      embed_visible: false,
      notes: "",
    }
  }
  return {
    name: t.name,
    slug: t.slug,
    family: t.family || "other",
    source_kind: t.source_kind || "direct",
    channel_id: t.channel_id ? String(t.channel_id) : "",
    source_group_id:
      t.source_group_id != null ? String(t.source_group_id) : "",
    source_group_name: t.source_group_name || "",
    base_url: t.base_url || "",
    api_key_source: t.api_key_source || "manual",
    api_key: "",
    protocol: t.protocol || "openai_chat",
    auth_style: t.auth_style || "bearer",
    proxy_enabled: Boolean(t.proxy_enabled),
    user_agent: t.user_agent || "",
    extra_headers_json: t.extra_headers_json || "",
    sort_order: String(t.sort_order ?? 0),
    enabled: Boolean(t.enabled),
    embed_visible: Boolean(t.embed_visible),
    notes: t.notes || "",
  }
}

export function TargetFormDialog({
  open,
  onOpenChange,
  target,
  onSaved,
}: TargetFormDialogProps) {
  const isEdit = Boolean(target)
  const channels = useChannels()
  const [form, setForm] = useState<FormState>(() => initialForm(target))
  const [groups, setGroups] = useState<ChannelAPIKeyGroup[]>([])
  const [loadingGroups, setLoadingGroups] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [ensuringKey, setEnsuringKey] = useState(false)
  const [keyHint, setKeyHint] = useState(target?.api_key_hint || "")
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (open) {
      setForm(initialForm(target))
      setKeyHint(target?.api_key_hint || "")
      setError(null)
    }
  }, [open, target])

  // 选中监控渠道时尝试拉取上游分组供快速选择。
  useEffect(() => {
    if (!open || form.source_kind !== "channel" || !form.channel_id) {
      setGroups([])
      return
    }
    let cancelled = false
    setLoadingGroups(true)
    apiFetch<ChannelAPIKeyGroup[]>(`/channels/${form.channel_id}/api-keys/groups`)
      .then((res) => {
        if (!cancelled) setGroups(Array.isArray(res) ? res : [])
      })
      .catch(() => {
        if (!cancelled) setGroups([])
      })
      .finally(() => {
        if (!cancelled) setLoadingGroups(false)
      })
    return () => {
      cancelled = true
    }
  }, [open, form.source_kind, form.channel_id])

  async function handleEnsureKey() {
    if (!target) return
    setEnsuringKey(true)
    try {
      const updated = await apiFetch<ProbeTarget>(
        `/intelligence/targets/${target.id}/ensure-key`,
        { method: "POST" },
      )
      setKeyHint(updated.api_key_hint || "")
      toast.success(`已托管上游专用 Key：${updated.api_key_hint || updated.api_key_name}`)
      onSaved(updated)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "托管 Key 失败")
    } finally {
      setEnsuringKey(false)
    }
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setError(null)

    if (!form.name.trim()) {
      setError("请填写监测目标名称")
      return
    }
    if (form.source_kind === "channel" && !form.channel_id) {
      setError("请选择要绑定的监控渠道")
      return
    }
    if (form.source_kind === "direct" && !form.base_url.trim()) {
      setError("直连模式必须填写 Base URL")
      return
    }
    if (form.extra_headers_json.trim()) {
      try {
        const parsed = JSON.parse(form.extra_headers_json)
        if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
          throw new Error("格式必须为 JSON 对象")
        }
      } catch (err) {
        setError(
          `自定义请求头 JSON 无效：${err instanceof Error ? err.message : "解析失败"}`,
        )
        return
      }
    }

    const payload: ProbeTargetInput = {
      name: form.name.trim(),
      slug: form.slug.trim() || undefined,
      family: form.family,
      source_kind: form.source_kind,
      channel_id: form.source_kind === "channel" ? Number(form.channel_id) : 0,
      source_group_id:
        form.source_kind === "channel" && form.source_group_id.trim()
          ? Number(form.source_group_id)
          : null,
      source_group_name:
        form.source_kind === "channel" ? form.source_group_name.trim() : "",
      base_url: form.base_url.trim(),
      api_key_source:
        form.source_kind === "channel" ? form.api_key_source : "manual",
      api_key: form.api_key.trim() || undefined,
      protocol: form.protocol,
      auth_style: form.auth_style,
      proxy_enabled: form.proxy_enabled,
      user_agent: form.user_agent.trim() || undefined,
      extra_headers_json: form.extra_headers_json.trim() || undefined,
      sort_order: Number(form.sort_order || 0),
      enabled: form.enabled,
      embed_visible: form.embed_visible,
      notes: form.notes.trim() || undefined,
    }

    setSubmitting(true)
    try {
      const saved = isEdit
        ? await apiFetch<ProbeTarget>(`/intelligence/targets/${target!.id}`, {
            method: "PUT",
            body: JSON.stringify(payload),
          })
        : await apiFetch<ProbeTarget>("/intelligence/targets", {
            method: "POST",
            body: JSON.stringify(payload),
          })
      toast.success(
        isEdit
          ? `已更新监测目标 ${saved.name}`
          : `已创建监测目标 ${saved.name}（请在插件矩阵中勾选开启检测项）`,
      )
      onSaved(saved)
      onOpenChange(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存失败")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {isEdit ? `编辑监测目标 · ${target?.name}` : "新增监测目标"}
          </DialogTitle>
          <DialogDescription>
            监测目标完全独立于转发网关。新建目标默认不自动开启定时测活，保存后请在「插件配置」中按需启用。
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="pt-name">目标名称</Label>
              <Input
                id="pt-name"
                placeholder="例如：codex-plus / claude-max"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                required
                disabled={submitting}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="pt-slug">URL 标识 Slug（可选）</Label>
              <Input
                id="pt-slug"
                placeholder="留空自动生成，如 codex-plus"
                value={form.slug}
                onChange={(e) => setForm({ ...form, slug: e.target.value })}
                disabled={submitting}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="pt-family">模型家族标签</Label>
              <Select
                value={form.family}
                onValueChange={(v) =>
                  setForm({ ...form, family: v as ProbeFamily })
                }
                disabled={submitting}
              >
                <SelectTrigger id="pt-family" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(Object.keys(FAMILY_META) as ProbeFamily[]).map((k) => (
                    <SelectItem key={k} value={k}>
                      {FAMILY_META[k].label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="pt-order">排序权重（越小越靠前）</Label>
              <Input
                id="pt-order"
                type="number"
                value={form.sort_order}
                onChange={(e) =>
                  setForm({ ...form, sort_order: e.target.value })
                }
                disabled={submitting}
              />
            </div>
          </div>

          <div className="space-y-3 rounded-lg border border-border p-3.5">
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="pt-source">来源模式</Label>
                <Select
                  value={form.source_kind}
                  onValueChange={(v) => {
                    const kind = v as ProbeSourceKind
                    setForm({
                      ...form,
                      source_kind: kind,
                      api_key_source:
                        kind === "channel" ? form.api_key_source : "manual",
                    })
                  }}
                  disabled={submitting}
                >
                  <SelectTrigger id="pt-source" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="direct">直连端点（BaseURL + Key）</SelectItem>
                    <SelectItem value="channel">绑定已有监控渠道</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="pt-proto">探测协议</Label>
                <Select
                  value={form.protocol}
                  onValueChange={(v) => {
                    const proto = v as ProbeProtocol
                    setForm({
                      ...form,
                      protocol: proto,
                      auth_style:
                        proto === "anthropic" ? "x-api-key" : "bearer",
                    })
                  }}
                  disabled={submitting}
                >
                  <SelectTrigger id="pt-proto" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {(Object.keys(PROTOCOL_LABELS) as ProbeProtocol[]).map(
                      (p) => (
                        <SelectItem key={p} value={p}>
                          {PROTOCOL_LABELS[p]}
                        </SelectItem>
                      ),
                    )}
                  </SelectContent>
                </Select>
              </div>
            </div>

            {form.source_kind === "channel" ? (
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="pt-ch">绑定监控渠道</Label>
                  <Select
                    value={form.channel_id}
                    onValueChange={(v) =>
                      setForm({
                        ...form,
                        channel_id: v,
                        source_group_id: "",
                        source_group_name: "",
                      })
                    }
                    disabled={submitting}
                  >
                    <SelectTrigger id="pt-ch" className="w-full">
                      <SelectValue placeholder="请选择监控渠道" />
                    </SelectTrigger>
                    <SelectContent>
                      {(channels.data ?? []).map((c) => (
                        <SelectItem key={c.id} value={String(c.id)}>
                          {c.name} ({c.type})
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>

                <div className="space-y-1.5">
                  <Label htmlFor="pt-group">
                    绑定上游分组（可选）
                    {loadingGroups ? " · 加载中..." : ""}
                  </Label>
                  {groups.length > 0 ? (
                    <Select
                      value={form.source_group_name || "__none__"}
                      onValueChange={(v) => {
                        if (v === "__none__") {
                          setForm({
                            ...form,
                            source_group_id: "",
                            source_group_name: "",
                          })
                          return
                        }
                        const found = groups.find((g) => g.name === v)
                        setForm({
                          ...form,
                          source_group_name: v,
                          source_group_id:
                            found?.id != null ? String(found.id) : "",
                        })
                      }}
                      disabled={submitting}
                    >
                      <SelectTrigger id="pt-group" className="w-full">
                        <SelectValue placeholder="不指定分组" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="__none__">默认分组</SelectItem>
                        {groups.map((g) => (
                          <SelectItem key={`${g.id ?? "n"}-${g.name}`} value={g.name}>
                            {g.name}
                            {g.description ? ` (${g.description})` : ""}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  ) : (
                    <Input
                      id="pt-group"
                      placeholder="可手动填写分组名，留空使用默认"
                      value={form.source_group_name}
                      onChange={(e) =>
                        setForm({ ...form, source_group_name: e.target.value })
                      }
                      disabled={submitting}
                    />
                  )}
                </div>

                <div className="space-y-1.5">
                  <Label htmlFor="pt-key-src">密钥来源</Label>
                  <Select
                    value={form.api_key_source}
                    onValueChange={(v) =>
                      setForm({
                        ...form,
                        api_key_source: v as ProbeAPIKeySource,
                      })
                    }
                    disabled={submitting}
                  >
                    <SelectTrigger id="pt-key-src" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="auto_channel_key">
                        自动托管渠道专用 Key
                      </SelectItem>
                      <SelectItem value="manual">手动填写 API Key</SelectItem>
                    </SelectContent>
                  </Select>
                </div>

                <div className="space-y-1.5">
                  <Label htmlFor="pt-base-override">
                    覆写 Base URL（可选，默认用渠道地址）
                  </Label>
                  <Input
                    id="pt-base-override"
                    placeholder="留空自动继承监控渠道站点地址"
                    value={form.base_url}
                    onChange={(e) =>
                      setForm({ ...form, base_url: e.target.value })
                    }
                    disabled={submitting}
                  />
                </div>
              </div>
            ) : (
              <div className="space-y-1.5">
                <Label htmlFor="pt-base-url">Base URL</Label>
                <Input
                  id="pt-base-url"
                  placeholder="https://api.example.com"
                  value={form.base_url}
                  onChange={(e) =>
                    setForm({ ...form, base_url: e.target.value })
                  }
                  required
                  disabled={submitting}
                />
              </div>
            )}

            <div className="grid gap-3 sm:grid-cols-2">
              {form.source_kind === "direct" ||
              form.api_key_source === "manual" ? (
                <div className="space-y-1.5">
                  <Label htmlFor="pt-key">
                    API Key
                    {isEdit && target?.api_key_hint
                      ? `（当前 ${target.api_key_hint}，留空保留原值）`
                      : ""}
                  </Label>
                  <Input
                    id="pt-key"
                    type="password"
                    placeholder={isEdit ? "留空则不修改密钥" : "sk-..."}
                    value={form.api_key}
                    onChange={(e) =>
                      setForm({ ...form, api_key: e.target.value })
                    }
                    disabled={submitting}
                  />
                </div>
              ) : (
                <div className="flex flex-col justify-end space-y-1.5">
                  <Label>托管状态</Label>
                  <div className="flex items-center gap-2">
                    <span className="truncate text-xs text-muted-foreground">
                      {keyHint
                        ? `已托管 ${keyHint}`
                        : "保存后可一键在渠道创建专用监测 Key"}
                    </span>
                    {isEdit ? (
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        onClick={handleEnsureKey}
                        disabled={ensuringKey || submitting}
                        className="shrink-0 gap-1"
                      >
                        {ensuringKey ? (
                          <Loader2 className="size-3.5 animate-spin" />
                        ) : (
                          <KeyRound className="size-3.5" />
                        )}
                        一键托管 Key
                      </Button>
                    ) : null}
                  </div>
                </div>
              )}

              <div className="space-y-1.5">
                <Label htmlFor="pt-auth-style">鉴权头格式</Label>
                <Select
                  value={form.auth_style}
                  onValueChange={(v) =>
                    setForm({ ...form, auth_style: v as ProbeAuthStyle })
                  }
                  disabled={submitting}
                >
                  <SelectTrigger id="pt-auth-style" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="bearer">Authorization: Bearer</SelectItem>
                    <SelectItem value="x-api-key">x-api-key</SelectItem>
                    <SelectItem value="both">同时发送两者</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="pt-ua">自定义 User-Agent（可选）</Label>
              <Input
                id="pt-ua"
                placeholder="留空使用全局上游 UA"
                value={form.user_agent}
                onChange={(e) =>
                  setForm({ ...form, user_agent: e.target.value })
                }
                disabled={submitting}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="pt-headers">额外请求头 JSON（可选）</Label>
              <Input
                id="pt-headers"
                placeholder='{"X-Custom-Header":"value"}'
                value={form.extra_headers_json}
                onChange={(e) =>
                  setForm({ ...form, extra_headers_json: e.target.value })
                }
                disabled={submitting}
              />
            </div>
          </div>

          <div className="grid gap-2.5 sm:grid-cols-3">
            <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2">
              <div>
                <p className="text-xs font-medium">启用</p>
                <p className="text-[11px] text-muted-foreground">停用暂停调度与公开</p>
              </div>
              <Switch
                checked={form.enabled}
                onCheckedChange={(v) => setForm({ ...form, enabled: v })}
                disabled={submitting}
              />
            </div>
            <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2">
              <div>
                <p className="text-xs font-medium">对外 Embed</p>
                <p className="text-[11px] text-muted-foreground">允许嵌入展示</p>
              </div>
              <Switch
                checked={form.embed_visible}
                onCheckedChange={(v) => setForm({ ...form, embed_visible: v })}
                disabled={submitting}
              />
            </div>
            <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2">
              <div>
                <p className="text-xs font-medium">走系统代理</p>
                <p className="text-[11px] text-muted-foreground">使用全局代理</p>
              </div>
              <Switch
                checked={form.proxy_enabled}
                onCheckedChange={(v) => setForm({ ...form, proxy_enabled: v })}
                disabled={submitting}
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="pt-notes">备注（仅内部可见）</Label>
            <Textarea
              id="pt-notes"
              rows={2}
              placeholder="记录账号来源、分组说明等，绝不会暴露到外部 Embed"
              value={form.notes}
              onChange={(e) => setForm({ ...form, notes: e.target.value })}
              disabled={submitting}
            />
          </div>

          {error ? (
            <p className="text-sm text-destructive" role="alert">
              {error}
            </p>
          ) : null}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={submitting}
            >
              取消
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? "保存中..." : "保存目标"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
