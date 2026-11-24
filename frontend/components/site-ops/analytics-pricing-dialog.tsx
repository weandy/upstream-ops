import { useState, type FormEvent } from "react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type {
  SiteOpsAnalyticsConfig, SiteOpsAnalyticsCurrency, SiteOpsAnalyticsPriceScope,
  SiteOpsAnalyticsPricingResponse, SiteOpsAnalyticsPutReferencePriceInput, SiteOpsAnalyticsReferencePriceView,
} from "@/lib/site-ops-types"
import { selectClass } from "./analytics-users-table"

export type PriceTarget = { scope: SiteOpsAnalyticsPriceScope; id: number }
export type PricingChange =
  | { kind: "config"; input: SiteOpsAnalyticsConfig }
  | { kind: "price"; target: PriceTarget; input: SiteOpsAnalyticsPutReferencePriceInput }
  | { kind: "delete"; target: PriceTarget; generation: number }

function scaledInteger(text: string, digits: number) {
  const parts = text.trim().split(".")
  if (parts.length > 2 || !/^\d+$/.test(parts[0]) || !/^\d*$/.test(parts[1] ?? "") || (parts[1]?.length ?? 0) > digits) return NaN
  return Number(parts[0] + (parts[1] ?? "").padEnd(digits, "0"))
}

function PriceForm({ target, row, generation, onSave }: {
  target: PriceTarget; row?: SiteOpsAnalyticsReferencePriceView; generation: number; onSave: (change: PricingChange) => void
}) {
  const [currency, setCurrency] = useState<SiteOpsAnalyticsCurrency>(row?.currency ?? "CNY")
  const [amount, setAmount] = useState(row ? (row.monthly_minor / 100).toFixed(2) : "")
  const [fx, setFX] = useState(row?.cny_per_usd_micros ? String(row.cny_per_usd_micros / 1e6) : "")
  const [error, setError] = useState("")
  function submit(e: FormEvent) {
    e.preventDefault()
    const minor = scaledInteger(amount, 2)
    const micros = currency === "CNY" && fx.trim() ? scaledInteger(fx, 6) : undefined
    if (!Number.isSafeInteger(minor) || minor < 0 || minor > 1e9) return setError("参考月收款范围为 0 至 1000 万，最多两位小数。")
    if (micros !== undefined && (!Number.isSafeInteger(micros) || micros < 1 || micros > 1e9)) return setError("行级汇率须大于 0 且不超过 1000，最多六位小数。")
    setError("")
    onSave({ kind: "price", target, input: { generation, currency, monthly_minor: minor, cny_per_usd_micros: micros } })
  }
  return <form className="space-y-3" onSubmit={submit}>
    <label className="grid gap-1">币种<select className={selectClass} value={currency} onChange={e => setCurrency(e.target.value as SiteOpsAnalyticsCurrency)}><option>CNY</option><option>USD</option></select></label>
    <label className="grid gap-1">参考月收款（{currency}）<Input required inputMode="decimal" value={amount} onChange={e => setAmount(e.target.value)} placeholder="填 0 表示赠送" /></label>
    {currency === "CNY" && <label className="grid gap-1">行级汇率（CNY / USD）<Input inputMode="decimal" value={fx} onChange={e => setFX(e.target.value)} placeholder="留空使用实例汇率" /></label>}
    {error && <p role="alert" className="text-destructive">{error}</p>}
    <div className="flex flex-wrap justify-between gap-2">
      <Button type="button" variant="outline" disabled={!row} onClick={() => onSave({ kind: "delete", target, generation })}>
        {target.scope === "subscription" ? "清除覆盖，恢复分组默认" : "删除分组默认价"}
      </Button>
      <Button type="submit">保存参考收款</Button>
    </div>
  </form>
}

function ConfigForm({ config, onSave }: { config: SiteOpsAnalyticsConfig; onSave: (change: PricingChange) => void }) {
  const [fx, setFX] = useState(String(config.cny_per_usd))
  const [warn, setWarn] = useState(String(config.leverage_warn_threshold))
  const [danger, setDanger] = useState(String(config.leverage_danger_threshold))
  const [error, setError] = useState("")
  function submit(e: FormEvent) {
    e.preventDefault()
    const [rate, warning, critical] = [fx, warn, danger].map(v => v.trim() ? Number(v) : NaN)
    if (![rate, warning, critical].every(Number.isFinite) || rate <= 0 || rate > 100 || warning < 0 || warning > 1000 || critical < 0 || critical > 1000 || (critical > 0 && critical < warning)) {
      setError("汇率范围为 (0, 100]，阈值范围为 0 至 1000。启用危险阈值时须大于等于预警阈值。")
      return
    }
    setError("")
    onSave({ kind: "config", input: { cny_per_usd: rate, leverage_warn_threshold: warning, leverage_danger_threshold: critical } })
  }
  return <form className="space-y-3" onSubmit={submit}>
    <label className="grid gap-1">实例汇率（CNY / USD）<Input type="number" step="any" required value={fx} onChange={e => setFX(e.target.value)} /></label>
    <label className="grid gap-1">杠杆预警阈值（倍）<Input type="number" step="any" required value={warn} onChange={e => setWarn(e.target.value)} /></label>
    <label className="grid gap-1">杠杆危险阈值（倍）<Input type="number" step="any" required value={danger} onChange={e => setDanger(e.target.value)} /></label>
    <p className="text-xs text-muted-foreground">阈值填 0 可关闭对应风险上色。</p>
    {error && <p role="alert" className="text-destructive">{error}</p>}
    <Button type="submit">保存实例设置</Button>
  </form>
}

export function AnalyticsPricingDialog({ initial, data, busy, error, onClose, onSave }: {
  initial?: PriceTarget; data?: SiteOpsAnalyticsPricingResponse; busy: boolean; error?: string
  onClose: () => void; onSave: (change: PricingChange) => void
}) {
  const [scope, setScope] = useState<SiteOpsAnalyticsPriceScope>(initial?.scope ?? "group")
  const [idText, setIdText] = useState(initial ? String(initial.id) : "")
  const id = /^\d+$/.test(idText) ? Number(idText) : NaN
  const row = data?.prices?.find(p => p.scope === scope && p.scope_id === id)
  return <Dialog open onOpenChange={open => { if (!open && !(busy && data)) onClose() }}>
    <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-md">
      <DialogHeader><DialogTitle>参考收款与汇率设置</DialogTitle><DialogDescription>本地参考价用于折算订阅收款与消耗杠杆。订阅覆盖优先于分组默认。</DialogDescription></DialogHeader>
      {busy && <p role="status" className="text-sm">{data ? "正在保存…" : "正在读取本地配置…"}</p>}
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      {data && <fieldset disabled={busy || error === "站点配置已变更，请重新打开"} className="min-w-0 text-sm">
        <Tabs defaultValue={initial ? "price" : "config"}><TabsList className="w-full">
          <TabsTrigger value="price">参考月收款</TabsTrigger><TabsTrigger value="config">实例汇率与阈值</TabsTrigger>
        </TabsList>
          <TabsContent value="price" className="space-y-3">
            <div className="grid grid-cols-2 gap-2"><label className="grid gap-1">生效范围
              <select className={selectClass} value={scope} onChange={e => { setScope(e.target.value as SiteOpsAnalyticsPriceScope); setIdText("") }}>
                <option value="group">分组默认</option><option value="subscription">订阅覆盖</option>
              </select></label>
              <label className="grid gap-1">{scope === "group" ? "分组 ID" : "订阅 ID"}
                <Input inputMode="numeric" list="analytics-price-ids" value={idText} onChange={e => setIdText(e.target.value)} />
                <datalist id="analytics-price-ids">{data.prices?.filter(p => p.scope === scope).map(p => <option key={p.scope_id} value={p.scope_id} />)}</datalist>
              </label></div>
            {Number.isSafeInteger(id) && id > 0 ? <PriceForm key={`${scope}:${id}`} target={{ id, scope }} generation={data.generation} row={row} onSave={onSave} /> : <p className="text-muted-foreground">请输入正整数 ID 以编辑该项参考价。</p>}
          </TabsContent>
          <TabsContent value="config"><ConfigForm config={data.config} onSave={onSave} /></TabsContent>
        </Tabs>
      </fieldset>}
    </DialogContent>
  </Dialog>
}
