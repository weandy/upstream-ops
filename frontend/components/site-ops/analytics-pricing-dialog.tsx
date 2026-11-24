import { useState, type FormEvent } from "react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type {
  SiteOpsAnalyticsConfig,
  SiteOpsAnalyticsCurrency,
  SiteOpsAnalyticsPriceScope,
  SiteOpsAnalyticsPricingResponse,
  SiteOpsAnalyticsPutReferencePriceInput,
  SiteOpsAnalyticsReferencePriceView,
} from "@/lib/site-ops-types"

const selectClass = "h-9 rounded-md border bg-background px-2 text-sm"

export type PriceTarget = { scope: SiteOpsAnalyticsPriceScope; id: number }
export type PricingChange =
  | { kind: "config"; input: SiteOpsAnalyticsConfig }
  | { kind: "price"; target: PriceTarget; input: SiteOpsAnalyticsPutReferencePriceInput }
  | { kind: "delete"; target: PriceTarget; generation: number }

function scaledInteger(text: string, digits: number) {
  const parts = text.trim().split(".")
  if (
    parts.length > 2 ||
    !/^\d+$/.test(parts[0]) ||
    !/^\d*$/.test(parts[1] ?? "") ||
    (parts[1]?.length ?? 0) > digits
  ) {
    return NaN
  }
  return Number(parts[0] + (parts[1] ?? "").padEnd(digits, "0"))
}

function ForecastRulesForm({
  config,
  onSave,
}: {
  config: SiteOpsAnalyticsConfig
  onSave: (change: PricingChange) => void
}) {
  const [nearFull, setNearFull] = useState(String(config.near_full_pct ?? 85))
  const [underUse, setUnderUse] = useState(String(config.under_use_pct ?? 35))
  const [highConc, setHighConc] = useState(String(config.high_concurrency_pct ?? 85))
  const [unlimCap, setUnlimCap] = useState(String(config.unlimited_target_usd ?? 400))
  const [error, setError] = useState("")

  function submit(e: FormEvent) {
    e.preventDefault()
    const [n, l, c, u] = [nearFull, underUse, highConc, unlimCap].map((v) =>
      v.trim() ? Number(v) : NaN,
    )
    if (
      ![n, l, c, u].every(Number.isFinite) ||
      n < 50 ||
      n > 99 ||
      l < 5 ||
      l > 70 ||
      l >= n ||
      c < 50 ||
      c > 100 ||
      u < 10 ||
      u > 100000
    ) {
      setError("接近用满 50~99%，偏低 5~70% 且须小于接近用满，并发高载 50~100%，期望上限 10~100000。")
      return
    }
    setError("")
    onSave({
      kind: "config",
      input: {
        cny_per_usd: config.cny_per_usd || 7.2,
        leverage_warn_threshold: config.leverage_warn_threshold ?? 3,
        leverage_danger_threshold: config.leverage_danger_threshold ?? 5,
        near_full_pct: n,
        under_use_pct: l,
        high_concurrency_pct: c,
        unlimited_target_usd: u,
      },
    })
  }

  function resetDefaults() {
    setNearFull("85")
    setUnderUse("35")
    setHighConc("85")
    setUnlimCap("400")
    setError("")
  }

  return (
    <form className="space-y-3" onSubmit={submit}>
      <div className="grid grid-cols-2 gap-3">
        <label className="grid gap-1 text-xs">
          <span className="text-muted-foreground">接近用满门槛 %</span>
          <Input
            type="number"
            step="1"
            min={50}
            max={99}
            required
            value={nearFull}
            onChange={(e) => setNearFull(e.target.value)}
          />
        </label>
        <label className="grid gap-1 text-xs">
          <span className="text-muted-foreground">用量偏低门槛 %</span>
          <Input
            type="number"
            step="1"
            min={5}
            max={70}
            required
            value={underUse}
            onChange={(e) => setUnderUse(e.target.value)}
          />
        </label>
        <label className="grid gap-1 text-xs">
          <span className="text-muted-foreground">并发高载门槛 %</span>
          <Input
            type="number"
            step="1"
            min={50}
            max={100}
            required
            value={highConc}
            onChange={(e) => setHighConc(e.target.value)}
          />
        </label>
        <label className="grid gap-1 text-xs">
          <span className="text-muted-foreground">不限额套餐期望上限 USD</span>
          <Input
            type="number"
            step="10"
            min={10}
            max={100000}
            required
            value={unlimCap}
            onChange={(e) => setUnlimCap(e.target.value)}
          />
        </label>
      </div>
      <p className="text-xs text-muted-foreground">
        刚开通不足 2 天固定标待观察；已过周期满 30% 才会判定用量偏低。保存后立即按新门槛重算所有订阅预测标签。
      </p>
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
      <div className="flex items-center justify-between gap-2 pt-1">
        <Button type="button" variant="outline" size="sm" onClick={resetDefaults}>
          恢复默认
        </Button>
        <Button type="submit" size="sm">
          保存预测规则
        </Button>
      </div>
    </form>
  )
}

function PriceForm({
  target,
  row,
  generation,
  onSave,
}: {
  target: PriceTarget
  row?: SiteOpsAnalyticsReferencePriceView
  generation: number
  onSave: (change: PricingChange) => void
}) {
  const [currency, setCurrency] = useState<SiteOpsAnalyticsCurrency>(row?.currency ?? "CNY")
  const [amount, setAmount] = useState(row ? (row.monthly_minor / 100).toFixed(2) : "")
  const [fx, setFX] = useState(row?.cny_per_usd_micros ? String(row.cny_per_usd_micros / 1e6) : "")
  const [error, setError] = useState("")

  function submit(e: FormEvent) {
    e.preventDefault()
    const minor = scaledInteger(amount, 2)
    const micros = currency === "CNY" && fx.trim() ? scaledInteger(fx, 6) : undefined
    if (!Number.isSafeInteger(minor) || minor < 0 || minor > 1e9) {
      return setError("参考月收款范围为 0 至 1000 万，最多两位小数。")
    }
    if (micros !== undefined && (!Number.isSafeInteger(micros) || micros < 1 || micros > 1e9)) {
      return setError("汇率须大于 0 且不超过 1000，最多六位小数。")
    }
    setError("")
    onSave({
      kind: "price",
      target,
      input: { generation, currency, monthly_minor: minor, cny_per_usd_micros: micros },
    })
  }

  return (
    <form className="space-y-3" onSubmit={submit}>
      <label className="grid gap-1 text-xs">
        <span className="text-muted-foreground">币种</span>
        <select
          className={selectClass}
          value={currency}
          onChange={(e) => setCurrency(e.target.value as SiteOpsAnalyticsCurrency)}
        >
          <option>CNY</option>
          <option>USD</option>
        </select>
      </label>
      <label className="grid gap-1 text-xs">
        <span className="text-muted-foreground">参考月收款 · {currency}</span>
        <Input
          required
          inputMode="decimal"
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          placeholder="填 0 表示赠送"
        />
      </label>
      {currency === "CNY" && (
        <label className="grid gap-1 text-xs">
          <span className="text-muted-foreground">自定义汇率 · CNY / USD</span>
          <Input
            inputMode="decimal"
            value={fx}
            onChange={(e) => setFX(e.target.value)}
            placeholder="留空按站点默认汇率"
          />
        </label>
      )}
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
      <div className="flex flex-wrap justify-between gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={!row}
          onClick={() => onSave({ kind: "delete", target, generation })}
        >
          {target.scope === "subscription" ? "恢复分组默认" : "删除分组默认价"}
        </Button>
        <Button type="submit" size="sm">
          保存参考收款
        </Button>
      </div>
    </form>
  )
}

export function AnalyticsPricingDialog({
  initial,
  data,
  busy,
  error,
  onClose,
  onSave,
}: {
  initial?: PriceTarget
  data?: SiteOpsAnalyticsPricingResponse
  busy: boolean
  error?: string
  onClose: () => void
  onSave: (change: PricingChange) => void
}) {
  const [scope, setScope] = useState<SiteOpsAnalyticsPriceScope>(initial?.scope ?? "group")
  const [idText, setIdText] = useState(initial ? String(initial.id) : "")
  const id = /^\d+$/.test(idText) ? Number(idText) : NaN
  const row = data?.prices?.find((p) => p.scope === scope && p.scope_id === id)

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !(busy && data)) onClose() }}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle>预测规则设置</DialogTitle>
          <DialogDescription>
            调整订阅周期消耗预测与并发高载判定门槛，也可按需维护分组参考月收款。
          </DialogDescription>
        </DialogHeader>
        {busy && <p role="status" className="text-sm">{data ? "正在保存…" : "正在读取配置…"}</p>}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        {data && (
          <fieldset
            disabled={busy || error === "站点配置已变更，请重新打开"}
            className="min-w-0 text-sm"
          >
            <Tabs defaultValue={initial ? "price" : "rules"}>
              <TabsList className="w-full">
                <TabsTrigger value="rules">预测与提醒规则</TabsTrigger>
                <TabsTrigger value="price">参考月收款</TabsTrigger>
              </TabsList>
              <TabsContent value="rules" className="pt-2">
                <ForecastRulesForm config={data.config} onSave={onSave} />
              </TabsContent>
              <TabsContent value="price" className="space-y-3 pt-2">
                <div className="grid grid-cols-2 gap-2">
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">生效范围</span>
                    <select
                      className={selectClass}
                      value={scope}
                      onChange={(e) => {
                        setScope(e.target.value as SiteOpsAnalyticsPriceScope)
                        setIdText("")
                      }}
                    >
                      <option value="group">分组默认</option>
                      <option value="subscription">单条订阅</option>
                    </select>
                  </label>
                  <label className="grid gap-1 text-xs">
                    <span className="text-muted-foreground">
                      {scope === "group" ? "分组 ID" : "订阅 ID"}
                    </span>
                    <Input
                      inputMode="numeric"
                      list="analytics-price-ids"
                      value={idText}
                      onChange={(e) => setIdText(e.target.value)}
                    />
                    <datalist id="analytics-price-ids">
                      {data.prices
                        ?.filter((p) => p.scope === scope)
                        .map((p) => (
                          <option key={p.scope_id} value={p.scope_id} />
                        ))}
                    </datalist>
                  </label>
                </div>
                {Number.isSafeInteger(id) && id > 0 ? (
                  <PriceForm
                    key={`${scope}:${id}`}
                    target={{ id, scope }}
                    generation={data.generation}
                    row={row}
                    onSave={onSave}
                  />
                ) : (
                  <p className="text-xs text-muted-foreground">请输入正整数 ID 编辑该项参考收款。</p>
                )}
              </TabsContent>
            </Tabs>
          </fieldset>
        )}
      </DialogContent>
    </Dialog>
  )
}
