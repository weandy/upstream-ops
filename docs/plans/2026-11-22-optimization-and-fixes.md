# UpstreamOps 全面优化与缺陷修复计划

> **适用范围**：`weandy/upstream-ops`（基于提交 `e9da0ec` 之后）
> **工作原则**：严格遵守根目录 [AGENTS.md](../../AGENTS.md) 全部红线；按大板块分步实施，每个板块遵循「补齐/先写失败测试 → 最小实现修复 → 测试与实机验证 → 本地 `git commit`（不 `push`）」。

## 执行顺序与风险策略

按「先保正确性与并发安全 → 再消性能与持久化瓶颈 → 再加固安全与交互 → 最后补工程化与文档」的顺序分 **5 个大板块** 推进，每完成一个大板块即验证并做一次语义清晰的本地提交：

1. **板块 0：规划文档与本地调试工具**（建立问题台账 + 一键编译/重启/探活/诊断调试脚本）
2. **板块 1（P0）：并发、正确性与边界缺陷修复**（最高风险，修完立刻杜绝跨目标误复用、panic 死锁、丢轮与停机漏停）
3. **板块 2（P1）：性能、存储查询与告警持久化优化**（消除 N+1 SQL 放大、修复前端长挂内存泄漏、持久化告警冷却与统一 UTC+8）
4. **板块 3（P2）：安全边界与前端/嵌入体验优化**（设置脱敏与原子写盘、登录防爆破限流、429 保留白名单、多目标并发手动测）
5. **板块 4（P3）：工程化、CI 质量门禁、Dockerfile 与文档同步**（全量回归 + 重编重启本地服务验证闭环）

---

## 红线约束（严禁回退或触碰）

- 测活默认 60 秒；`candy`/`lmfpd`/`pelican`/`custom_prompt` 默认 3600 秒；调度 tick 5 秒；按锚点加间隔推进。
- 可用率条固定 60 格，单格等于测活间隔，文案「近 N 分钟」，单行等高胶囊。
- 画鹈鹕内置「完整 HTML + 内联 SVG 动画」原文，不改 3 小时，不为了画廊去重删除历史记录。
- 不做收入、`/api/log/stat`、销售或大客户分析；不改 Sub2API 登录，不重做凭证加密。
- 不改流结束标记判断与 2MiB 捕获上限；`auth.enabled` 默认关闭，`/api/auth/me` 仅 401 进登录页。
- 严禁提交 `config.yaml`、`data/`、`*.exe`、`frontend/node_modules/`、`web/dist/*`、`.env`；严禁打印 `APP_SECRET`；严禁 `push`。

---

## 详细修复清单

### 板块 0：台账与本地调试工具

- [x] **T0-1** 创建本优化修复计划文档 `docs/plans/2026-11-22-optimization-and-fixes.md`
- [x] **T0-2** 创建本地开发与调试脚本 `scripts/dev-debug.ps1`（支持 `build`、`start`、`stop`、`restart`、`status`、`test`、`verify`、`diag`；强制 `GOTOOLCHAIN=go1.23.0` + `CGO_ENABLED=0`，从 `config.yaml` 静默读取 `APP_SECRET` 且绝不打印）

---

### 板块 1（P0）：并发、正确性与边界缺陷修复

- [x] **P0-1：修复 `computeSharedCacheKey` 自定义请求头大小写查找错位**
  - **位置**：`backend/intelligence/runner.go`（`computeSharedCacheKey`）
  - **问题**：排序后的 `headerKeys` 为小写，但拼接时却用 `ep.ExtraHeaders[k]` 取值，导致含大写字母的自定义 Header 值恒为空，跨目标错误复用缓存。
  - **方案**：先构造 `lowerKey -> trimmedValue` 规范化映射，再排序拼接。
  - **验证**：`backend/intelligence/runner_test.go` 新增大小写 Header 区分与规范化单测。

- [x] **P0-2：修复两处自研 `singleflight` 缺少 `defer` 防 panic 死锁，并隔离手动与定时 `ctx` 取消**
  - **位置**：`backend/intelligence/runner.go`（`inflightGroup.Do`、`ExecuteDetector`）、`backend/embed/cache.go`（`singleflightGroup.Do`）
  - **问题**：`fn()` 若 panic 会跳过 `wg.Done()` 与 `delete(g.m, key)`，引发同 key 永久死锁；手动探测若与定时任务合流，一方取消 `ctx` 会误伤另一方。
  - **方案**：为 `inflightGroup.Do` 与 `singleflightGroup.Do` 增加 `defer` 保证异常必释放锁与 map 条目；`ExecuteDetector` 手动探测跳过 `inflight` 合并（遵循「手动运行独立发请求」语义），或定时合流时与调用方解耦超时。
  - **验证**：新增 panic 恢复防死锁单测与手动/定时隔离单测，通过 `go test -race`。

- [x] **P0-3：修复 `dispatchDueJobs` 先推进 `next_run_at` 再抢并发槽导致的漏轮问题**
  - **位置**：`backend/intelligence/runner.go`（`dispatchDueJobs`）
  - **问题**：先写库推进 `next_run_at` 再等待 `sem`，若并发满载期间触发 `Reconfigure` 取消 `tickCtx`，该任务已改写下次执行时间却未实际运行，白白跳过一整轮。
  - **方案**：先 `select` 拿到 `sem`（或响应 `tickCtx.Done()` 返回），拿到槽位后再调用 `UpdateSchedule` 推进 `next_run_at`；若 `UpdateSchedule` 失败则释放 `sem`。
  - **验证**：新增 `Reconfigure`/取消时未拿到槽位的任务保留原 `next_run_at` 的单测。

- [x] **P0-4：修复 `SaveTargetDetectors` 缩短检测间隔后未收敛 `next_run_at`**
  - **位置**：`backend/intelligence/runner.go`（`SaveTargetDetectors`）
  - **问题**：注释说明「从禁用切换为启用、或缩短间隔时，立即安排下一轮执行」，但实现漏掉了缩短间隔分支。
  - **方案**：当已启用插件将 `interval` 调小且原 `next_run_at` 晚于 `now + newInterval` 时，收敛 `next_run_at`（如 `last_run_at + newInterval`，若已过期则设为 `now`）。
  - **验证**：新增缩短间隔自动收敛 `next_run_at` 且不破坏原锚点语义的单测。

- [x] **P0-5：修复 `runtimeconfig.Manager` 并发热重载竞态与停机遗漏最新 `scheduler`**
  - **位置**：`backend/runtimeconfig/runtime.go`、`cmd/server/main.go`
  - **问题**：`ApplyFromFile()` 无串行互斥、在 `newScheduler.Start()` 失败前提前修改各服务导致半应用；`main.go` 停机 `defer sch.Stop()` 只停初始实例而非热重载后的最新实例。
  - **方案**：增加 `applyMu` 串行化 `ApplyFromFile()`；将校验与 `newScheduler.Start()` 前置，全部成功后再切换各服务配置与替换 `m.scheduler`；暴露 `runtimeMgr.Stop()` 在 `main.go` 中优雅停止最新调度器。
  - **验证**：`backend/runtimeconfig/runtime_test.go` 新增失败不污染现网配置、并发应用安全与 `Stop()` 停止新调度器的单测。

---

### 板块 2（P1）：性能、存储查询与告警持久化优化

- [ ] **P1-1：消除概览与公开快照的 N+1 SQL 查询放大**
  - **位置**：`backend/storage/intelligence.go`、`backend/intelligence/service.go`、`backend/embed/snapshot.go`
  - **问题**：`ListTargetOverviews` 每目标约 8 次 SQL（含拉 24h 样本后又重复查最近 20 条）；`BuildSiteSnapshot` 同样逐目标循环查库，SQLite 单连接下读放大严重。
  - **方案**：在仓储层新增批量查询接口（按 `target_id IN (...)` 查 `ProbeDetectorConfig`、`ProbeLivenessSample`、各插件最新 `ProbeIntelligenceRun` 及 24h Candy 记录），在内存中按 `target_id` 分组聚合；`RecentSamples` 直接从已查的 `samples` 切片尾部倒序取最近 20 条（不足时再回退查库）。
  - **验证**：新增批量查询与行为等价单测，确保管理台与公开页数据结构完全一致。

- [ ] **P1-2：修复前端 `useApi` 缓存 `Map` 只增不删的内存泄漏**
  - **位置**：`frontend/lib/queries.ts`
  - **问题**：`cacheKey(path, tick, bump)` 随每次 30s 轮询递增生成新 key，历史条目永不清理，长挂监控页会持续累积内存。
  - **方案**：写入/读取时清理过期条目，且缓存按 `path` 存储（附带 `tick`/`bump`/`expiresAt`），同一 `path` 覆盖旧值，杜绝无限增长。
  - **验证**：通过 `pnpm exec tsc --noEmit --incremental false` 与 `pnpm lint`。

- [ ] **P1-3：告警冷却持久化 + 重启掉线恢复感知 + 通知时间统一 UTC+8**
  - **位置**：`backend/intelligence/alerter.go`、`cmd/server/main.go`
  - **问题**：`Alerter` 的 `cooldown` 和 `downSet` 仅存内存，重启会丢冷却或漏恢复通知；正文时间直接用服务器本地时区。
  - **方案**：支持注入持久化冷却仓储（复用 `notification_cooldowns` 表，使用 `intel:<event>:<detector>` 独立事件命名空间，保留内存兜底便于轻量单测）；测活恢复时除内存 `downSet` 外，若曾记录过持续冷却/连续失败也可正确发恢复并清冷却；通知正文时间统一按 `Asia/Shanghai`（UTC+8）格式化。
  - **验证**：`backend/intelligence/alerter_test.go` 新增跨实例（模拟重启）冷却生效、恢复清冷却与 UTC+8 时间格式单测。

---

### 板块 3（P2）：安全边界与前端/嵌入体验优化

- [ ] **P2-1：系统设置敏感字段脱敏回显 + `config.Save` 权限收紧与原子写入**
  - **位置**：`backend/api/settings.go`、`backend/config/config.go`、`frontend/app/settings-page.tsx`、`frontend/lib/api-types.ts`
  - **问题**：`GET /api/settings/config` 明文回传 `auth.password`、`auth.tokenSecret`、`proxy.password`；`config.Save` 使用 `0o644` 直接覆写文件。
  - **方案**：
    - `GET /api/settings/config` 不回传明文密码/密钥，改为返回空字符串 + `secrets_status` 布尔标记（如 `auth_password_set`、`auth_token_secret_set`、`proxy_password_set`）；
    - `PUT /api/settings/config` 留空时默认保留配置文件里的原密码/密钥（并支持显式清除开关如清空代理密码）；
    - 前端设置页展示「已设置（留空保留原值）」占位提示，不影响正常修改；
    - `config.Save` 改为同目录临时文件 + `0o600` 权限 + 原子重命名替换（兼容 Windows）。
  - **验证**：`backend/api/settings_test.go`、`backend/config/config_test.go` 新增脱敏/留空保留/原子写盘单测；前端 `tsc` + `lint` 通过。

- [ ] **P2-2：登录接口按 IP 防爆破限流 + 移除 URL Query `?token=` 鉴权入口**
  - **位置**：`backend/auth/auth.go`、`backend/api/auth.go`
  - **问题**：开启鉴权后 `/api/auth/login` 无防爆破限制；`extractToken` 接受 `?token=` 易泄露至日志与历史记录（已核查全前端均使用 `Authorization: Bearer` 头，无任何 `?token=` 调用）。
  - **方案**：新增轻量按 IP 登录失败计数器与指数退避/冷却限制（有界容量防 OOM，登录成功后清零，超限返回 `429` + `Retry-After`）；移除 `extractToken` 中的 Query `token` 提取，仅保留 `Authorization: Bearer` 与 `uh_token` Cookie。
  - **验证**：新增 `backend/auth/auth_test.go` 覆盖防爆破限流、成功复位、拒绝 Query Token 与接受 Bearer/Cookie。

- [ ] **P2-3：公开 `/embed` 触发 429 时保留对应站点的 `frame-ancestors` 白名单**
  - **位置**：`backend/embed/ratelimit.go`
  - **问题**：429 响应调用 `ApplyEmbedHeaders(c, nil)`，跨域嵌入站触发限流时会因 `frame-ancestors 'self'` 被浏览器拦截成白屏/拒绝连接。
  - **方案**：在 HTML 429 分支尝试从缓存中轻量查找当前 `?site=` 对应的 `AllowedOrigins`（不穿透查库避免被 429 流量反打数据库），命中则写入该站点的 `frame-ancestors`。
  - **验证**：`backend/embed/ratelimit_test.go` 新增带白名单站点触发 429 后响应头仍含合法 `frame-ancestors` 的单测。

- [ ] **P2-4：前端多目标独立手动触发状态**
  - **位置**：`frontend/app/intelligence-page.tsx`
  - **问题**：`runningTargetID` 为单个 `number | null`，长耗时目标（如画鹈鹕）运行期间无法独立展示/触发其他目标的运行态。
  - **方案**：升级为 `Set<number>`（`runningTargetIDs`），各目标按钮独立 loading、互不阻塞。
  - **验证**：前端 `tsc`、`pnpm lint`、`pnpm build` 全部通过。

---

### 板块 4（P3）：工程化、CI 质量门禁、Dockerfile 与文档同步

- [ ] **P3-1：补齐 GitHub Actions CI 质量门禁**
  - **位置**：`.github/workflows/ci.yml`、`.github/workflows/publish.yml`
  - **方案**：新增 `ci.yml` 在 push / PR 时执行 Go 全量测试（`go test ./...`）与前端类型检查、ESLint、构建；同时在 `publish.yml` 的 `build-and-push` 前增加 `verify` 前置校验 job，未通过不发镜像。

- [ ] **P3-2：加固 `Dockerfile` 并清理重复锁文件**
  - **位置**：`Dockerfile`、删除多余的 `frontend/package-lock.json`
  - **方案**：前端阶段改用 `pnpm install --frozen-lockfile`；运行阶段新增非 root 用户 `upstreamops`（确保 `/app/data` 权限）与基于 `http://127.0.0.1:8418/healthz` 的 `HEALTHCHECK`；清理未使用的 `frontend/package-lock.json`（保留 `pnpm-lock.yaml`）。

- [ ] **P3-3：同步文档漂移**
  - **位置**：`README.zh.md`、`README.md`
  - **方案**：更新可用率条说明为「60 格 × 测活间隔（近 N 分钟）」，修正前端本地开发访问地址为 `http://localhost:3010/`（说明 Vite 仅绑 localhost）。

---

## 最终验收清单

- [ ] `GOTOOLCHAIN=go1.23.0 CGO_ENABLED=0 go test ./...` 全部通过
- [ ] `cd frontend && pnpm exec tsc --noEmit --incremental false && pnpm lint && pnpm build` 全部通过
- [ ] 使用 `scripts/dev-debug.ps1 -Action restart` 重编并重启本地 8418 后端，`diag` 探活全通过
- [ ] `git status` 干净，所有提交按板块清晰拆分且未执行 `push`
