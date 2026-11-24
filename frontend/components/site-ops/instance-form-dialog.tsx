import { useState, type FormEvent } from "react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { createSiteOpsInstance, isSiteOpsAuthRequiredError, updateSiteOpsInstance } from "@/lib/site-ops-api"
import type { SiteOpsInstanceView } from "@/lib/site-ops-types"

export function InstanceFormDialog({
  instance, onClose, onSaved, onAuthRequired,
}: {
  instance: SiteOpsInstanceView | null
  onClose: () => void
  onSaved: (instance: SiteOpsInstanceView) => void
  onAuthRequired: () => void
}) {
  const [enabled, setEnabled] = useState(instance?.enabled ?? true)
  const [proxyEnabled, setProxyEnabled] = useState(instance?.proxy_enabled ?? false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (saving) return
    const form = new FormData(event.currentTarget)
    const name = String(form.get("name") ?? "").trim()
    const slug = String(form.get("slug") ?? "").trim()
    const baseUrl = String(form.get("base_url") ?? "").trim()
    const apiKey = String(form.get("api_key") ?? "").trim()
    const budget = String(form.get("audit_budget_per_day") ?? "")
    const keyKind = form.get("key_kind") === "full" ? "full" : "scoped"
    if (!name || (!instance && !apiKey)) {
      setError("请填写名称和必需的 API Key。")
      return
    }
    try {
      const url = new URL(baseUrl)
      if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || url.search || url.hash) {
        setError("请输入 HTTP 或 HTTPS 站点地址，地址中请勿包含凭据、查询参数或片段。")
        return
      }
    } catch {
      setError("请输入有效的站点地址。")
      return
    }
    const fields = {
      name,
      slug: slug || undefined,
      base_url: baseUrl,
      key_kind: keyKind,
      proxy_enabled: proxyEnabled,
      enabled,
      poll_interval_seconds: Number(form.get("poll_interval_seconds")),
      audit_budget_per_day: budget === "" ? undefined : Number(budget),
      sort_order: Number(form.get("sort_order")),
    } as const

    setSaving(true)
    setError("")
    try {
      const saved = instance
        ? await updateSiteOpsInstance(instance.id, { ...fields, api_key: apiKey || undefined })
        : await createSiteOpsInstance({ ...fields, api_key: apiKey })
      onSaved(saved)
    } catch (err) {
      if (isSiteOpsAuthRequiredError(err)) {
        onAuthRequired()
      } else {
        setError("保存失败。请检查站点地址白名单、名称和 Slug 是否重复，以及各项配置。")
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !saving) onClose() }}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{instance ? "编辑自有站点" : "添加自有站点"}</DialogTitle>
          <DialogDescription>
            站点地址须命中部署侧的 <code>siteOps.allowedOrigins</code> 或 <code>siteOps.allowedCIDRs</code> 白名单。
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={save} className="space-y-4">
          <fieldset disabled={saving} className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="site-ops-name">名称</Label>
              <Input id="site-ops-name" name="name" defaultValue={instance?.name} required maxLength={128} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="site-ops-slug">Slug</Label>
              <Input id="site-ops-slug" name="slug" defaultValue={instance?.slug} placeholder="portal" maxLength={64} />
            </div>
            <div className="space-y-1.5 sm:col-span-2">
              <Label htmlFor="site-ops-url">Base URL</Label>
              <Input id="site-ops-url" name="base_url" type="url" defaultValue={instance?.base_url} placeholder="https://portal.example.com" required maxLength={512} />
            </div>
            <div className="space-y-1.5 sm:col-span-2">
              <Label htmlFor="site-ops-key">Admin API Key</Label>
              <Input id="site-ops-key" name="api_key" type="password" autoComplete="new-password" required={!instance} aria-describedby={instance ? "site-ops-key-hint" : undefined} />
              {instance && (
                <p id="site-ops-key-hint" className="text-xs text-muted-foreground">
                  API Key 留空将保留已加密的旧密钥。当前提示 {instance.api_key_hint || "无"}
                </p>
              )}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="site-ops-key-kind">Key 类型</Label>
              <select id="site-ops-key-kind" name="key_kind" defaultValue={instance?.key_kind === "full" ? "full" : "scoped"} className="h-9 w-full rounded-md border border-input bg-background px-2 text-sm">
                <option value="scoped">受限权限（scoped）</option>
                <option value="full">完整权限（full）</option>
              </select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="site-ops-interval">轮询间隔（秒）</Label>
              <Input id="site-ops-interval" name="poll_interval_seconds" type="number" min={30} defaultValue={instance?.poll_interval_seconds ?? 60} required />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="site-ops-budget">每日审计调用预算</Label>
              <Input id="site-ops-budget" name="audit_budget_per_day" type="number" min={0} defaultValue={instance?.audit_budget_per_day} placeholder="使用部署默认值" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="site-ops-sort">排序权重</Label>
              <Input id="site-ops-sort" name="sort_order" type="number" defaultValue={instance?.sort_order ?? 0} required />
            </div>
            <div className="flex items-center gap-2">
              <Switch id="site-ops-proxy" checked={proxyEnabled} onCheckedChange={setProxyEnabled} disabled={saving} />
              <Label htmlFor="site-ops-proxy">使用全局代理</Label>
            </div>
            <div className="flex items-center gap-2">
              <Switch id="site-ops-enabled" checked={enabled} onCheckedChange={setEnabled} disabled={saving} />
              <Label htmlFor="site-ops-enabled">启用轮询</Label>
            </div>
          </fieldset>
          {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={saving} onClick={onClose}>取消</Button>
            <Button type="submit" disabled={saving}>{saving ? "保存中…" : "保存站点"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
