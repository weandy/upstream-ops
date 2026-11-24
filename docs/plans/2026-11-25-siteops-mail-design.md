# `/site-ops` 营销邮件与客户触达子功能模块（Wave 5）最终架构与开发设计规格书

> **文档状态**：**最终定稿（v1.0）** — 已完成「Neon 独立方案 + Codex 独立方案 → Codex 源码级交叉评审（4 Blocker / 5 High / 2 Medium）→ Neon 终审裁决」全流程闭环，作为后续多 worktree 施工与验收的唯一准绳  
> **所属仓库**：`D:\AI\upstream-ops`（核验基线：`main@71504c0`）  
> **参考思想来源**：`D:\AI\transit-hub` 营销邮件模块（仅汲取三层领域建模与 `at-most-once` 防骚扰理念，零机械搬运）  
> **技术栈硬约束**：Go 1.23+（Gin + GORM + SQLite WAL `MaxOpenConns=1` / MySQL / PostgreSQL，**零新增 Go 依赖**，不修改 `go.mod`/`go.sum`）+ React 19 + TypeScript + Tailwind CSS v4 + 既有 `frontend/components/ui/*`（**零新增 npm 依赖**，不修改 `frontend/package.json`/`pnpm-lock.yaml`）

---

## 第 0 章：交叉评审终审裁决记录（Decision Record D1–D10）

本章逐项收口 Codex 源码级交叉评审发现的全部 4 项 Blocker（B1–B4）、5 项 High（H1–H5）与 2 项 Medium（M1–M2），锁定唯一落地方案。

| 裁决 | 争议与问题摘要 | 终审唯一裁决（施工准绳） |
| :--- | :--- | :--- |
| **D1 源码事实校正** | 两稿对 `SiteOpsStore` 静默 logger、`ParseSubscriptionsPage` 含明文邮箱、`Service.Stop()` 持锁与 `DeleteInstance` 先删父行存在偏差。 | ① `NewSiteOpsStore` 已用静默 GORM logger，新增邮件表全部通过 `SiteOpsStore` 会话访问，同时 `AutoMigrate` 与错误上抛不透传 SQL 参数原文；② 用户页与订阅页均存在明文邮箱，必须建立邮件专用私有投影；③ `DeleteInstance` 改为**先锁父行 → 删子表 → 删父行**；④ `Service.Stop()` 先在锁内切停机态并取引用，**释放 `Service.mu` 后再等 worker 退出**。 |
| **D2 入口门禁（解决 B1）** | `auth.enabled` 默认 `false` 且本地经 Vite `:3010 → :8418` 代理；Codex 提议增加离线 CLI 证明（安全最高但个人本地使用每次需开终端兑换），Neon 提议四重 HTTP 回环校验（零摩擦但无法识别同机剥头反代）。 | **分层务实裁决（消除 TOCTOU + 零额外 CLI 摩擦 + 明确部署红线）**：<br>1. **在 `backend/api/api.go` 全局 `AuthMiddleware` 之前**为所有 `/api/site-ops/instances/*/mail*` 路径统一写入 `Cache-Control: no-store`（确保 401/403 均不缓存）；<br>2. **管理员分支**：仅认本请求 `auth.Service.Middleware()` 验证成功后写入 Gin Context 的 `authSubject`（彻底消除 `auth.enabled` 热切换 TOCTOU）；<br>3. **无全局鉴权分支（`authSubject` 缺失且 `runtime.CurrentAuth() == nil`）**：执行**五重严格本机回环守卫**：① 真实 TCP 对端 `IsLoopbackRemoteAddr(c.Request.RemoteAddr)` 为真；② 拒绝**任何**转发头（`Forwarded`、`X-Forwarded-*`、`X-Real-IP`、`X-Client-IP`、`CF-Connecting-IP`、`True-Client-IP`、`Via`）；③ `Host` 头去端口后必须为 `localhost`/`127.0.0.1`/`::1`；④ 浏览器 `Origin` 若存在必须为 `http://(localhost\|127.0.0.1\|[::1])(:port)?`，且拒绝 `Sec-Fetch-Site: cross-site`；⑤ 非 `GET`/`HEAD` 写操作必须为 `application/json`（阻断简单表单 CSRF）。任一不满足返回 `403 SITE_OPS_MAIL_AUTH_REQUIRED`；<br>4. **安全说明与自托管红线**：当 `auth.enabled == false` 时前端邮件顶栏显式展示本机回环模式提示；公网/反代部署（含同机剥头反代）必须开启 `auth.enabled: true`（因为裸露管理台本身还可访问 `/api/settings`）。不引入额外的 `cmd/siteops-mail-local` CLI 工具；<br>5. 邮件路由组内捕获 panic 并静态记录，绝不将请求头/请求体输出到日志。 |
| **D3 模板与预览（解决 B4）** | Codex 9 标签 XHTML 无法承载营销卡片；Neon 仅做模板 AST 校验与 iframe CSP 无法清洗实际发出邮件中的静态 HTML/CSS。 | **卡片 HTML + 零依赖服务端 DOM 白名单重建 + 9 项安全变量**：<br>1. 在 `backend/siteops/mail_html.go` 实现零第三方依赖的 HTML 子集词法/树解析与白名单重建器（不修改 `go.mod`）：保留营销卡片所需的 `table/thead/tbody/tfoot/tr/td/th/div/span/p/br/hr/h1..h4/strong/b/em/i/u/ul/ol/li/a`；<br>2. 仅允许白名单属性与安全内联 CSS 属性/值（颜色、背景色、字体、字号、行高、对齐、内外边距、宽高、边框、圆角、`display:block\|inline\|inline-block\|table`），严禁 `url(`、`@import`、`expression(`、`javascript:`、`data:`、`on*` 事件、`<script>`、`<style>`、`<iframe>`、`<form>`、`<object>`、`<img>`；<br>3. `a[href]` 仅允许合法 `https://` / `mailto:` 静态链接或完整 `{{.SiteURL}}`；<br>4. 9 项只读白名单变量（`SiteName`/`SiteURL`/`UserID`/`EmailMasked`/`BalanceUSD`/`PlanName`/`ExpiresDate`/`ExpiresInDays`/`TodayDate`），保存前静态 AST 校验，仅允许单字段插值与单字段 `if`；<br>5. 同一清洗渲染树同时输出：① 实际邮件 HTML；② 可读 `text/plain`（`multipart/alternative`）；③ 服务端 `POST .../templates/preview` 去导航预览副本（将 `<a>` 降级为不可点击样式文本），前端放入 `<iframe sandbox="" referrerPolicy="no-referrer">` 配合固定 CSP（仅放行 `style-src 'unsafe-inline'`，其余全 `'none'`）；<br>6. 可选 `List-Unsubscribe` 仅输出合法 `<https://...>` / `<mailto:...>`，**不自动附带 RFC 8058 `List-Unsubscribe-Post: One-Click`**（因本期不开放匿名退订接口）。 |
| **D4 六表存储、互斥与生命周期（解决 B2、H2、H4）** | Neon 5 表无 per-scope 随机种子且 `100 KiB` 超 MySQL `TEXT` 上限；Codex 6 表同时维护 `scope.ActiveBatchID` 与 `batch` 状态存在双写对账风险且改址清指针会允许旧站 DATA 在途与新站并发。 | **定 6 表 + 唯一 `ActiveSlot` 互斥 + 两阶段 DATA 授权 + MySQL 字节安全上限**：<br>1. 6 张 GORM 表：`site_ops_mail_scopes`、`site_ops_mail_smtp`、`site_ops_mail_templates`、`site_ops_mail_suppressions`、`site_ops_mail_batches`、`site_ops_mail_items`；<br>2. `SiteOpsMailScope` 仅保存 `(InstanceID, TargetBinding)` 唯一随机 32 字节种子密文 `SeedCipher`（**不放 `ActiveBatchID`**），实例级单活跃互斥**唯一由 `SiteOpsMailBatch.ActiveSlot *uint`（可空唯一索引）保证**；<br>3. 改址/换钥/禁用/删除在实例更新事务内线性化取消活跃批次、擦除非 `data` 阶段密文；若改址时旧批仍有已获 `DATA` 许可的在途项，`ActiveSlot` 保持占用直至在途项结算或 10m 回收才释放，彻底消除新旧并发；改址事务同步清除旧 `SiteOpsMailSMTP`，保留按 `TargetBinding` 隔离的模板与退订（A→B→A 自动恢复 A 站退订）；<br>4. 固定锁顺序：**实例 ID 升序 → scope → SMTP/模板 → batch ID 升序 → item ID 升序**；<br>5. 尺寸适配 MySQL `TEXT`（65,535 字节）：单模板 `HTMLBody <= 24 KiB`、`TextBody <= 12 KiB`、批快照 `SnapshotJSON <= 60 KiB`，应用层显式赋初值避免 MySQL `TEXT DEFAULT ''` DDL 坑；<br>6. 过期历史留存默认 `SiteOpsMailBatchesDays = 30`（`0` 关闭），接入 `scheduler` 三处钩子分块清理（先子后父，每次 200 子项，总上限 10,000 行 / 2s），永久保留 SMTP、模板、种子与退订。 |
| **D5 五输出面隐私与密钥（解决 B3、H2）** | Neon 的 `VarsJSON` 明文存余额/套餐/到期日、SMTP AAD 未绑主机账号、且 `APP_SECRET` 轮换会破坏退订哈希；Codex 的版本化对象密文 + per-scope 随机种子更稳健。 | **沿用现有 `crypto.Cipher`（不改旧加解密协议）+ 版本化对象密文 + per-scope 随机种子**：<br>1. `SiteOpsMailScope.SeedCipher` 保存 `crypto.Cipher` 密封的 32 字节随机种子，退订与收件人共享 `HMAC-SHA256(seed, "mail-addr-v1\x00" + canonical_lower_email)`；<br>2. SMTP 身份（`username` + `from_email`）与密码均以版本化 JSON 结构体密封（绑定 `purpose`, `instance_id`, `target_binding`, `host`, `port`, `revision`），解密后逐项核验；修改 `host`/`port`/`username` 必须重填密码（`auth_mode != "none"`）；<br>3. `SiteOpsMailItem.PayloadCipher *string` 将收件邮箱与该条所需的非静态模板变量（`BalanceUSD`/`PlanName`/`ExpiresDate`/`ExpiresInDays`）**一起密封**（绑定 `purpose`, `instance_id`, `target_binding`, `batch_id`, `user_id`, `email_hash`）；进入任意终态（`sent`/`failed`/`uncertain`/`cancelled`/`suppressed`）或取消时**同事务置 `PayloadCipher = NULL`**；<br>4. 用户页与订阅页均为邮件建立专用私有解析方法（禁入 ETag 缓存、禁入 `AnalyticsCache` LRU、禁入公共 `singleflight`），原始响应在解析栈内立即转成 `email_hash` + `email_masked` + `PayloadCipher` 后丢弃明文；<br>5. API 绝不回显明文收件邮箱、明文密码、明文用户名、`TargetBinding`、哈希或原始 SMTP 报错正文；`SMTPView` 返回 `from_masked`（脱敏发件人）、`username_configured` 与 `password_configured`。 |
| **D6 异步 `preparing` 与预算（解决 H1）** | 两稿对 `all`/`groups`/`segment` 的口径与预算保护细节需对齐。 | **五种受众模式统一口径 + 严格预算解耦**：<br>1. `selected`（1..500 去重正整数 UID，分页扫描找全即早停）；<br>2. `groups`（1..50 组 ID 并集，定义为 `asOf` 时刻 `status==active && starts_at<=asOf && expires_at>asOf` 的有效订阅用户）；<br>3. `segment`（Wave 4 的 6 标签 + 4 活跃度，同维 OR、跨维 AND；按需读取订阅/并发/快查用量；若请求的谓词依赖单用户 `usage/stats` 且超出预算/准备期或覆盖不全，**诚实中止并报 `MAIL_PREP_COVERAGE_INCOMPLETE`**，严禁把缺失数据补 0 判为 `low`/`dormant`）；<br>4. `all`（仅 `status==active` 且 `role∈{"user","member"}`，永久排除管理员、禁用、封禁及非法地址）；<br>5. `retry_failed`（仅从专用端点触发，读取同站同绑定终态源批明确 `failed` 子项，重扫上游核对 **`UserID` 与 `EmailHash` 双匹配** + 仍 `active` + 未退订，地址变更标排除不猜测；每源批至多 1 个补发子批）；<br>6. 拆分 `client.go` 的「低优先级预算/占槽」与「缓存策略」两个正交字段；硬上限：100 页/目录、10,000 扫描行/目录、2,000 唯一收件人、30 分钟准备期；保留原始 `total/page/has_more` 漂移检测；提供显式干跑 `POST .../audience/preview`（最多 5 页 / 5s，标明估算）。 |
| **D7 SMTP、两阶段 DATA 许可与测试发信（解决 B2、H5）** | 防止取消/退订与 `DATA` 竞态，精确区分确定未发与结果未知。 | **单批连接复用 + 令牌桶 + 两阶段 DATA 许可 + 精准证据矩阵**：<br>1. `OutboundGuard` 拨获准数值 IP + 强制 TLS 1.2+（`implicit`/`starttls`，禁明文降级）+ 升级后协商 `PLAIN`/`LOGIN`/`none`；<br>2. 每站可配 `rate_per_minute`（默认 30，1..300）、`max_concurrency`（默认 1，1..4）、`reconnect_every`（默认 50，1..200），全进程总 SMTP 连接上限 8；等待令牌后再领取；<br>3. **两阶段短事务**：① 领取事务将 `pending → sending (phase="envelope", claim_token)` → 事务外发 `MAIL FROM`/`RCPT TO`；② `DATA` 许可事务复核 scope/代际/SMTP revision/`claim_token`/非取消/非退订后置 `phase="data"` → 事务外发 `DATA`/写正文/`Close()`；③ 结案事务按 `(id, status='sending', claim_token)` CAS 落终态并同事务擦除 `PayloadCipher = NULL`；<br>4. 证据矩阵：`DATA` 许可前一切错误、以及 `DATA` 命令或正文终结收到明确 `*textproto.Error`（4xx/5xx）→ `failed`（可补发）；`250 OK` → `sent`（忽略 `QUIT` 错误，写库失败只重试落账不重发）；许可后失联/超时/EOF 或 `sending` 滞留 ≥10m 回收 → `uncertain`（永不自动重试或补发）；<br>5. 熔断：同 revision 出现明确配置/证书/认证错误（`guard`/`tls`/`auth`）立即停止本批新领取并标记剩余未发为 `failed`（`MAIL_SMTP_CONFIG_BLOCKED`）；连续 3 次网络瞬断同样触发批次中止；<br>6. 测试发信 `POST .../smtp/test` 仅发往**已保存的 `From` 地址**，复用同套批次/活跃槽/`request_id` 持久幂等与每实例每分钟最多 1 次限频，20s 内等待终态返回 `200`，超时返回 `202` 统一 DTO，绝不进入客户明细或补发。 |
| **D8 API 契约与乐观锁（解决 H3）** | 防止多窗口互相覆盖配置/模板、防同键异参篡改、防大 HTML 撑爆列表。 | **全部挂载 `/api/site-ops/instances/:id/mail/*`**：<br>1. 站点上下文带 `generation` + `cred_gen`；SMTP 与模板带独立整型 `revision` CAS（新建 `expected_revision=0`）；<br>2. 创建批次/补发/测试均校验 `request_id`（小写 UUID）+ 规范化 `RequestDigest`（同键同摘要幂等返回 `200`，同键异摘要返 `409`）；<br>3. 列表类接口（`templates`、`batches`、`items`、`suppressions`）采用 keyset 游标分页（`cursor` + `limit` → `{items, next_cursor}`），模板列表不回大 `html_body`；上游候选目录使用页码分页；<br>4. 跨站复制（`POST .../smtp/copy`、`POST .../templates/copy`）锁双站父行（按 ID 升序）、核验双方代际与 revision、重新加密并重验目标链接。 |
| **D9 前端 `/site-ops` 集成与 Tab 1 联动（解决 M1）** | 避免 Tab 1 越界传递明文邮箱、StrictMode 重复消费草稿、mail 403 清空整个站点页。 | **三处极简接入 + 局部隔离**：<br>1. `SiteOpsPage` 仅增第 6 Tab `mail`「营销邮件」、一次性 `mailDraft = { nonce, instanceId, generation, credGen, userIds, sourceLabel }` 与活跃批徽章；切站/删站/注销自动清草稿；<br>2. `AnalyticsUsersTable` 仅透传正整数 `userIds` 与诚实文案（`"当前页筛选命中 N 人"` / `"当前页已勾选 N 人"`），严禁传整行或明文邮箱；<br>3. `MailPanel` 用 `key={`${selected.id}:${selected.generation}:${selected.cred_gen}`}` 懒挂载、零 `useRefreshTick()` 订阅、用 ref 防 StrictMode 重复导入、仅清匹配 `nonce`；未保存编辑在切换前确认；<br>4. `mail` 组 `403` 仅在 `MailPanel` 内渲染本机/鉴权提示卡，绝不触发 `SiteOpsPage.requireAuth()`；<br>5. 仅当前处于可见的活跃批详情时以串行 `setTimeout` 3s 轮询，遇 429/5xx 指数退避，隐藏/切出/终态立即清定时器并 `AbortController.abort()`。 |
| **D10 并行 git worktree 排程（解决 M2）** | 兼顾零冲突、独立可编译测试与本机约 1.5 GiB 空闲内存约束。 | **4 步拓扑（每波最多 2 路并行 worktree，重型测试/构建串行执行）**：<br>• **Wave 0（单写者冻结契约）**：`storage/site_ops_mail_model.go` + `siteops/types_mail.go` + `frontend/lib/site-ops-mail-types.ts`；<br>• **Wave 1（2 路并行 worktree）**：`1A 存储与留存` || `1B 目录与协议引擎`；<br>• **Wave 2（2 路并行 worktree）**：`2A 后端装配与路由门禁` || `2B 前端子模块与 Tab 1 联动`；<br>• **Wave 3（Neon 审核 + 集成复验 + 本地编译部署）**。 |

---

## 第 1 章：模块定位、边界与总体架构

### 1.1 零侵入边界清单

- **绝不修改**：`frontend/components/monitor/monitor-header.tsx`、`frontend/src/main.tsx`、`/settings`、`/notifications`、`backend/notify/email.go`、`backend/notify/dispatcher.go`、`backend/connector/`、`backend/monitor/`、`backend/intelligence/`、`backend/gateway/`、`backend/embed/`、`go.mod`、`go.sum`、`frontend/package.json`、`frontend/pnpm-lock.yaml`。
- **完全自治于 `/site-ops` 实例上下文**：所有配置、模板、退订名单、受众、批次 100% 绑定当前选中的 `instance_id` + 当前规范化 `BaseURL` 的 `TargetBinding`；绝不向 `site_ops_events`、`site_ops_alert_deliveries`、`site_ops_snapshots` 或每日运维日报写入任何记录。

### 1.2 总体分层架构

```text
React 19 (/site-ops)
├── InstanceSwitcher (选定 instance_id / generation / cred_gen)
├── Tab 1 客户与订阅 (AnalyticsPanel → AnalyticsUsersTable)
│     └─ onComposeMail(userIds, sourceLabel) ──▶ SiteOpsPage.mailDraft (一次性 nonce)
└── Tab 6 营销邮件 (MailPanel, key="id:gen:credGen", 零 useRefreshTick)
      ├── 顶栏状态 + [SMTP 设置 Dialog] + [退订名单 Dialog]
      ├── 子视图 1：发起群发 (MailCompose: selected / groups / segment / all + 干跑预览 + 幂等确认)
      ├── 子视图 2：发送批次 (MailBatches: 游标列表 + 局部 3s 串行进度 + 脱敏明细 + 取消 + 补发失败项)
      └── 子视图 3：邮件模板 (MailTemplates: 内置/自定义库 + 9 变量 + 服务端安全预览 iframe sandbox="")
                          │ HTTP REST (Cache-Control: no-store 置于鉴权前)
                          ▼
backend/api/site_ops_mail.go (/api/site-ops/instances/:id/mail/*)
├── RequireMailAccessMiddleware (authSubject 管理员放行 | auth-off 五重严格本机回环守卫)
├── Panic 静态兜底中间件（不泄露头体）
└── 严格 JSON/Query 校验（限字节、禁未知字段、禁尾随数据、JS 安全整数）
                          │
                          ▼
backend/siteops/ (挂载于 siteops.Service，零破坏 NewDefaultService 签名)
├── mail_crypto.go    (版本化对象密封 + per-scope 32B 随机种子 HMAC + 裸邮箱严格校验)
├── mail_html.go      (零依赖 HTML 白名单树重建 + CSS 白名单 + HTML→Text + 去导航预览)
├── mail_template.go  (3 套内置卡片模板 + 9 项 AST 白名单变量 + RFC 2047 + multipart/alternative)
├── client_mail.go    (私有用户/订阅投影，绕过 ETag/LRU/公共单飞，走低优先级预算与共享 429)
├── mail_audience.go  (异步 preparing 引擎 + 干跑预览 + Wave 4 洞察复用 + 漂移/覆盖中止)
├── mail_smtp.go      (OutboundGuard 拨号 + TLS 1.2+ + PLAIN/LOGIN + 连接复用 + 令牌桶 + 阶段分类)
├── mail_worker.go    (租约 owner + 两阶段 DATA 许可 + 熔断 + 10m 回收 + 无死锁优雅停机)
└── mail_service.go   (全部业务用例编排 + 实例改址/禁用/删除联动)
                          │
                          ▼
backend/storage/ (静默 GORM Session · SQLite WAL MaxOpenConns=1 / MySQL / PostgreSQL)
├── 6 张新表 (site_ops_mail_{scopes,smtp,templates,suppressions,batches,items})
└── scheduler.go (SiteOpsMailBatchesDays=30 三处钩子分块清理过期终态批次)
```

---

## 第 2 章：数据模型、跨库并发不变量与留存清理

### 2.1 六张 GORM 模型定义（`backend/storage/site_ops_mail_model.go`）

所有时间统一 UTC；严禁将 DB 模型直接交给 `c.JSON`；所有字段更新均通过 `map[string]any` 或 `.Select(...)` 显式写列，防止 GORM 忽略零值/`false`/`NULL`；`TEXT` 列不声明 MySQL 易报错的 `default:''` DDL，由 Go 应用层显式赋值。

```go
package storage

import "time"

// SiteOpsMailScope 单站点目标绑定的独立加密域，持有 32 字节随机 HMAC 种子密文。
// 同一实例改址 A→B→A 时按 TargetBinding 隔离；切回 A 自动恢复 A 的退订哈希域。
type SiteOpsMailScope struct {
	InstanceID    uint      `gorm:"primaryKey;autoIncrement:false"`
	TargetBinding string    `gorm:"primaryKey;size:64;not null"`
	SeedCipher    string    `gorm:"type:text;not null"`
	CreatedAt     time.Time `gorm:"not null"`
	UpdatedAt     time.Time `gorm:"not null"`
}

func (SiteOpsMailScope) TableName() string { return "site_ops_mail_scopes" }

// SiteOpsMailSMTP 当前实例的 SMTP 发信配置（单实例单行，改址事务内销毁旧行防误用旧凭据）。
type SiteOpsMailSMTP struct {
	InstanceID     uint      `gorm:"primaryKey;autoIncrement:false"`
	TargetBinding  string    `gorm:"size:64;not null"`
	Revision       int64     `gorm:"not null"`
	Host           string    `gorm:"size:253;not null"`
	Port           int       `gorm:"not null"`
	TLSMode        string    `gorm:"size:12;not null"` // "implicit" | "starttls"
	AuthMode       string    `gorm:"size:8;not null"`  // "auto" | "plain" | "login" | "none"
	IdentityCipher string    `gorm:"type:text;not null"` // 密封 {purpose,instance_id,target_binding,host,port,revision,username,from_email}
	PasswordCipher string    `gorm:"type:text;not null"` // 密封 {purpose,instance_id,target_binding,host,port,revision,username,password}，无密码为空串
	HasUsername    bool      `gorm:"not null"`
	HasPassword    bool      `gorm:"not null"`
	FromMasked     string    `gorm:"size:160;not null"`
	FromName       string    `gorm:"size:120;not null"`
	ReplyTo        string    `gorm:"size:254;not null"`
	UnsubscribeURL string    `gorm:"size:512;not null"` // 可选同站 HTTPS 或 mailto: 静态退订地址
	RatePerMinute  int       `gorm:"not null"`          // 默认 30 [1, 300]
	MaxConcurrency int       `gorm:"not null"`          // 默认 1  [1, 4]
	ReconnectEvery int       `gorm:"not null"`          // 默认 50 [1, 200]
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (SiteOpsMailSMTP) TableName() string { return "site_ops_mail_smtp" }

// SiteOpsMailTemplate 目标绑定的邮件模板（每目标内置 3 套 + 自定义上限 50 套）。
type SiteOpsMailTemplate struct {
	ID            uint      `gorm:"primaryKey"`
	InstanceID    uint      `gorm:"not null;uniqueIndex:uniq_som_tpl_key,priority:1;index:idx_som_tpl_scope,priority:1"`
	TargetBinding string    `gorm:"size:64;not null;uniqueIndex:uniq_som_tpl_key,priority:2;index:idx_som_tpl_scope,priority:2"`
	TemplateKey   string    `gorm:"size:64;not null;uniqueIndex:uniq_som_tpl_key,priority:3"` // "builtin:<slug>" | "custom:<hex16>"
	Revision      int64     `gorm:"not null"`
	Name          string    `gorm:"size:96;not null"`
	Category      string    `gorm:"size:24;not null"` // "marketing" | "renewal" | "reactivation" | "announcement"
	Subject       string    `gorm:"size:255;not null"`
	HTMLBody      string    `gorm:"type:text;not null"` // <= 24 KiB（适配 MySQL TEXT）
	TextBody      string    `gorm:"type:text;not null"` // <= 12 KiB，可空（自动降级生成）
	IsBuiltIn     bool      `gorm:"not null"`
	CreatedAt     time.Time `gorm:"not null"`
	UpdatedAt     time.Time `gorm:"not null"`
}

func (SiteOpsMailTemplate) TableName() string { return "site_ops_mail_templates" }

// SiteOpsMailSuppression 目标绑定的本地退订/免打扰名单（永久零明文邮箱）。
type SiteOpsMailSuppression struct {
	ID            uint      `gorm:"primaryKey"`
	InstanceID    uint      `gorm:"not null;uniqueIndex:uniq_som_supp,priority:1;index:idx_som_supp_list,priority:1"`
	TargetBinding string    `gorm:"size:64;not null;uniqueIndex:uniq_som_supp,priority:2;index:idx_som_supp_list,priority:2"`
	EmailHash     string    `gorm:"size:64;not null;uniqueIndex:uniq_som_supp,priority:3"`
	EmailMasked   string    `gorm:"size:160;not null"`
	Reason        string    `gorm:"size:32;not null"` // "admin_added" | "user_unsubscribed" | "hard_bounce"
	CreatedAt     time.Time `gorm:"not null;index:idx_som_supp_list,priority:3"`
}

func (SiteOpsMailSuppression) TableName() string { return "site_ops_mail_suppressions" }

// SiteOpsMailBatch 营销/测试批次主表（终态后默认保留 30 天）。
// ActiveSlot 在活跃态 (preparing/queued/running/cancelling) 恒为 &InstanceID，
// 仅当全部 pending 与 in-flight DATA 归零进入终态时在同事务置 nil；跨三库严格保证每实例至多 1 个活跃批。
type SiteOpsMailBatch struct {
	ID                 uint       `gorm:"primaryKey"`
	InstanceID         uint       `gorm:"not null;uniqueIndex:uniq_som_req,priority:1;index:idx_som_batch_list,priority:1"`
	TargetBinding      string     `gorm:"size:64;not null;uniqueIndex:uniq_som_req,priority:2;index:idx_som_batch_list,priority:2"`
	RequestID          string     `gorm:"size:64;not null;uniqueIndex:uniq_som_req,priority:3"`
	RequestDigest      string     `gorm:"size:64;not null"`
	ActiveSlot         *uint      `gorm:"uniqueIndex:uniq_som_active_slot"` // 活跃 = &InstanceID；终态 = nil
	ParentBatchID      *uint      `gorm:"uniqueIndex:uniq_som_parent_retry"` // 每终态批至多 1 个补发子批（多 NULL 三库兼容）
	Generation         int64      `gorm:"not null"`
	CredGen            int64      `gorm:"not null"`
	SMTPRevision       int64      `gorm:"not null"`
	TemplateID         uint       `gorm:"not null"`
	TemplateRevision   int64      `gorm:"not null"`
	TemplateName       string     `gorm:"size:96;not null"`
	Mode               string     `gorm:"size:20;not null"` // "selected" | "groups" | "segment" | "all" | "retry_failed" | "test"
	SnapshotJSON       string     `gorm:"type:text;not null"` // <= 60 KiB: {template_name,subject,html,text,site_name,site_url,send_date}
	AudienceJSON       string     `gorm:"type:text;not null"` // 封闭选择器；终态清理手选 user_ids
	Status             string     `gorm:"size:24;not null;index:idx_som_work,priority:1"`
	AbortReason        string     `gorm:"size:64;not null"`
	Owner              string     `gorm:"size:36;not null"`
	LeaseUntil         *time.Time
	NextWorkAt         time.Time  `gorm:"not null;index:idx_som_work,priority:2"`
	PrepareBy          time.Time  `gorm:"not null"`
	Pages              int        `gorm:"not null"`
	Scanned            int        `gorm:"not null"`
	RecipientCount     int        `gorm:"not null"`
	ExcludedCount      int        `gorm:"not null"`
	DuplicateCount     int        `gorm:"not null"`
	SuppressedCount    int        `gorm:"not null"`
	SentCount          int        `gorm:"not null"`
	FailedCount        int        `gorm:"not null"`
	UncertainCount     int        `gorm:"not null"`
	CancelledCount     int        `gorm:"not null"`
	CreatedAt          time.Time  `gorm:"not null"`
	UpdatedAt          time.Time  `gorm:"not null"`
	StartedAt          *time.Time
	FinishedAt         *time.Time `gorm:"index:idx_som_finished"`
	CancelledAt        *time.Time
}

func (SiteOpsMailBatch) TableName() string { return "site_ops_mail_batches" }

// SiteOpsMailItem 单收件人投递记录。
// PayloadCipher 仅在 pending/sending 阶段非 nil，密封收件邮箱及个性化变量；进入任意终态同事务置 nil。
type SiteOpsMailItem struct {
	ID            uint       `gorm:"primaryKey"`
	InstanceID    uint       `gorm:"not null;index:idx_som_item_inst"`
	BatchID       uint       `gorm:"not null;uniqueIndex:uniq_som_item_addr,priority:1;index:idx_som_item_claim,priority:1"`
	UserID        int64      `gorm:"not null"` // test 批固定为 0，客户批 > 0
	EmailHash     string     `gorm:"size:64;not null;uniqueIndex:uniq_som_item_addr,priority:2"`
	EmailMasked   string     `gorm:"size:160;not null"`
	PayloadCipher *string    `gorm:"type:text"` // 密封 {purpose,instance_id,target_binding,batch_id,user_id,email_hash,email,vars}；终态必为 nil
	Status        string     `gorm:"size:16;not null;index:idx_som_item_claim,priority:2;index:idx_som_item_stale,priority:1"`
	Phase         string     `gorm:"size:16;not null"` // "" | "envelope" | "data" | "done"
	ClaimToken    string     `gorm:"size:36;not null"`
	SMTPStage     string     `gorm:"size:24;not null"`
	SMTPCode      int        `gorm:"not null"`
	ErrorCode     string     `gorm:"size:64;not null"`
	ClaimedAt     *time.Time `gorm:"index:idx_som_item_stale,priority:2"`
	SentAt        *time.Time
	FinishedAt    *time.Time
	CreatedAt     time.Time  `gorm:"not null"`
	UpdatedAt     time.Time  `gorm:"not null"`
}

func (SiteOpsMailItem) TableName() string { return "site_ops_mail_items" }
```

### 2.2 事务、锁顺序与生命周期线性化不变量

1. **严格固定锁序**：所有多表事务均按 **`site_ops_instances`（按 ID 升序）→ `site_ops_mail_scopes` → `site_ops_mail_smtp` / `site_ops_mail_templates` → `site_ops_mail_batches`（按 ID 升序）→ `site_ops_mail_items`（按 ID 升序）** 加锁。在 PostgreSQL/MySQL 使用 `clause.Locking{Strength: "UPDATE"}`，在 SQLite WAL（`MaxOpenConns=1`）使用短事务 CAS 并检查 `RowsAffected`。**严禁在任何事务内执行网络 I/O、令牌桶等待、worker 等待或回到根 `s.db`。**
2. **创建与单活互斥**：`CreateMailBatch` 锁实例父行 → 先查 `(instance_id, target_binding, request_id)`：若存在且 `request_digest` 相同返回原批（`idempotentReplay=true`），摘要不同返 `ErrMailIdempotencyConflict (409)`；若不存在，校验实例启用、代际匹配、SMTP revision & 模板 revision 匹配（`retry_failed` 锁源批校验终态且无 `parent_batch_id` 子批），再查是否存在 `ActiveSlot == InstanceID`（存在返 `ErrMailActiveBatchExists (409)`），最后写入 `status='preparing'`、`active_slot=&InstanceID`。遇唯一冲突回滚后重读准确区分「同键异参」与「已有活跃批」。
3. **两阶段 DATA 授权（根治取消/退订竞态）**：
   - **阶段 1（Claim）**：短事务校验实例状态/代际、批次 `running`、owner/lease 有效、SMTP revision 未变，按 `id ASC` 挑 1 条 `pending`；先查 `site_ops_mail_suppressions`，若已退订直接置 `suppressed` + `PayloadCipher=nil` 并重算批次计数；否则 CAS `pending → sending`（`phase="envelope"`, `claim_token=uuid`, `claimed_at=now`）。事务提交后解密执行 `MAIL FROM` + `RCPT TO`。
   - **阶段 2（AuthorizeDATA）**：发送 `DATA` 命令前再开短事务（同锁序），核验实例启用/代际、SMTP revision、批次仍为 `running`（未进入 `cancelling`）、`item.status=='sending' && item.phase=='envelope' && item.claim_token==token` 且邮箱仍未命中退订表 → 更新 `phase="data"` 并提交。若已被取消或退订，立即置 `cancelled`/`suppressed` + `PayloadCipher=nil`，事务外执行 `RSET`/关连接，**绝不发送 `DATA`**。
   - **阶段 3（Complete）**：收到 SMTP 证据后短事务按 `(id, status='sending', claim_token)` CAS 置终态（`sent`/`failed`/`uncertain`）+ **`PayloadCipher = nil` + `phase = "done"`**，`GROUP BY status` 重算批次计数；若 `pending + sending == 0`，同事务按终态规则落账（若 `cancelled_at != nil` 落 `cancelled`，否则全成功/退订落 `completed`，有失败或不确定且有成功落 `completed_with_errors`，零成功落 `failed`），清 `AudienceJSON` 手选 ID，并置 **`ActiveSlot = nil`**。
4. **实例变更与删除事务线性化**：
   - `SiteOpsStore.UpdateInstance` 同事务内：若 `Generation` 递增（包括改址、换 Key、禁用、改名），取消该实例所有活跃批次（`preparing`/`queued`/`running` → `cancelling`，批量将 `pending` 及 `sending+envelope` 置 `cancelled` + `PayloadCipher=nil`；若无 `phase='data'` 在途立即落终态 `cancelled` + `ActiveSlot=nil`，若仍有 `data` 在途则保持 `cancelling` + `ActiveSlot=&InstanceID` 直至在途结案或 10m 回收）；若 `BaseURL` 变更，同事务删除旧 `SiteOpsMailSMTP` 行。
   - `SiteOpsStore.DeleteInstance` 改为**先以 `FOR UPDATE` 锁 `SiteOpsInstance` 父行** → 顺序删 `SiteOpsMailItem` → `SiteOpsMailBatch` → `SiteOpsMailSuppression` → `SiteOpsMailTemplate` → `SiteOpsMailSMTP` → `SiteOpsMailScope` → 既有其他 `site_ops_*` 表 → 最后删 `SiteOpsInstance` 父行。
5. **优雅停机无死锁（`Service.Stop()`）**：
   - 在 `s.mu` 内置停机标志、取出 `mailWorker` 引用后**立刻 `s.mu.Unlock()`**；
   - 锁外调用 `mailWorker.Stop(8 * time.Second)`：立刻 `cancel` 所有 `preparing` 上下文与令牌等待、阻断新领取，等待已获 `DATA` 许可的在途发送最多 8 秒完成落账；超时先强制关闭活跃 SMTP socket 再 `wg.Wait()` 退出，避免任何持锁互等。
6. **三处留存钩子（`backend/config/config.go` & `backend/scheduler/scheduler.go`）**：
   - `RetentionConfig` 新增 `SiteOpsMailBatchesDays int`（YAML `siteOpsMailBatchesDays`，加载默认 `30`，显式 `0` 禁用）。
   - `scheduler.go` 的 `hasRetention()` 纳入 `r.SiteOpsMailBatchesDays > 0`；`runRetention()` 调用 `s.siteOps.DeleteFinishedMailBatchesBeforeChunked(ctx, cutoff, 200, 10000, 2*time.Second)`：仅扫 `active_slot IS NULL AND finished_at IS NOT NULL AND finished_at < cutoff`，先分块删 `site_ops_mail_items`（200/批），子项清净后删父批次；分块清理中批次不参与列表与补发；绝不删除 SMTP、模板、种子与退订表。

---

## 第 3 章：安全门禁、出站防 SSRF 与五输出面隐私白名单

### 3.1 入口双轨五重安全门禁（`backend/siteops/mail_access.go` & `backend/api/api.go`）

1. **前置 `Cache-Control: no-store`**：在 `backend/api/api.go` 注册 `api.Use(d.Runtime.AuthMiddleware())` **之前**注册轻量路径中间件：凡请求路径匹配 `/api/site-ops/instances/*/mail` 或 `/api/site-ops/instances/*/mail/*`，第一时间写入 `Cache-Control: no-store` 与 `Pragma: no-cache`。
2. **Panic 隔离**：在 `/instances/:id/mail` 子组最外层注册自定义 Recovery，捕获 panic 仅记录路由模板与固定错误码，返回 `500 {"code":"SITE_OPS_INTERNAL_ERROR","error":"internal error"}`，绝不将请求头/体打印到 stderr。
3. **`RequireMailAccessMiddleware(authActive func() bool)` 判定逻辑**：
   - 若 Gin Context 中存在非空字符串 `authSubject`（由 `auth.Service.Middleware()` 验签写入）→ 管理员已鉴权，写入 `X-SiteOps-Mail-Auth-Mode: authenticated` 并放行；
   - 若无 `authSubject` 但 `authActive()` 为 `true` → 拒绝 `401`（防御性兜底）；
   - 若 `authActive() == false`（未开启全局鉴权）→ 必须全部通过以下 **5 项检查**，任一失败立即返回 `403 {"code":"SITE_OPS_MAIL_AUTH_REQUIRED","error":"marketing mail requires auth.enabled=true or direct local loopback access"}`：
     1. `IsLoopbackRemoteAddr(c.Request.RemoteAddr) == true`；
     2. 以下任一代理转发头非空一律拒绝：`Forwarded`、`X-Forwarded-For`、`X-Forwarded-Host`、`X-Forwarded-Proto`、`X-Real-IP`、`X-Client-IP`、`CF-Connecting-IP`、`True-Client-IP`、`Via`；
     3. `c.Request.Host` 去端口后必须为 `localhost`、`127.0.0.1` 或 `::1`；
     4. 若携带 `Origin` 头，解析后 `Scheme == "http"` 且主机名必须为 `localhost`、`127.0.0.1` 或 `::1`；若 `Sec-Fetch-Site` 为 `cross-site` 一律拒绝；
     5. 非 `GET`/`HEAD` 请求的 `Content-Type` 媒体类型必须为 `application/json`。
   - 全部通过时写入 `X-SiteOps-Mail-Auth-Mode: local_loopback_only` 并放行。

### 3.2 SMTP 出站安全与地址校验

- **主机/端口校验（`OutboundGuard.ValidateSMTPHostPort`）**：仅允许合法 ASCII 域名或 IP 字面量、端口 `[1, 65535]`，拒绝 URL、userinfo、控制字符、IPv6 zone；复用 `OutboundGuard` 永久硬拦截云元数据（`169.254.0.0/16`、`100.100.100.200/32`、`fd00:ec2::254/128`）、链路本地、未指定与多播地址；默认仅放行公网单播 IP，内网/回环 SMTP 仅当显式配置 `allowLoopback` 或 `allowedCIDRs` 时豁免（**不继承 HTTP `allowedOrigins`**，防止 Sub2API HTTP 豁免静默扩大 SMTP 权限）。
- **每次建连实时解析并拨获准数值 IP**：新增 `OutboundGuard.DialSMTPContext(ctx, host, port)`，先 DNS 解析并过滤，逐个直拨数值 IP，TLS 握手固定 `ServerName: cfg.Host`、`MinVersion: tls.VersionTLS12`、`InsecureSkipVerify: false`。
- **裸邮箱严格规范（`NormalizeBareEmail`）**：先拒控制字符/CR/LF/空白/`<`/`>`/引号/逗号/分号，验证单 `@`、总长 `<= 254`、local `1..64`、ASCII 域名分段 `1..63` 且含 `.`；域转小写保留信封 local，业务去重与退订 HMAC 使用全小写折叠地址；经 `MaskEmailStrict` 生成掩码（若掩码失效直接拒绝）。

### 3.3 版本化密封加密与五输出面隐私白名单

- **密封信封（`backend/siteops/mail_crypto.go`）**：复用现有 `crypto.Cipher`（AES-256-GCM 随机 nonce，零破坏旧接口），每类密文均封装为固定 JSON 结构体后加密：
  - `scopeSeedEnvelope{V:1, Purpose:"mail_scope_seed", InstanceID, TargetBinding, SeedB64}`
  - `smtpIdentityEnvelope{V:1, Purpose:"mail_smtp_identity", InstanceID, TargetBinding, Host, Port, Revision, Username, FromEmail}`
  - `smtpPasswordEnvelope{V:1, Purpose:"mail_smtp_password", InstanceID, TargetBinding, Host, Port, Revision, Username, Password}`
  - `mailItemPayloadEnvelope{V:1, Purpose:"mail_item_payload", InstanceID, TargetBinding, BatchID, UserID, EmailHash, Email, Vars}`
  - **解封硬规则**：解密后必须逐项比对 `V`、`Purpose` 与所有外部可信上下文键（含重算 `EmailHash`），任一不符立即拒绝；同批不同行密文调换无法通过校验。
  - **主密钥轮换保护**：若 `SeedCipher` 无法解密，暂停该站发信并返回 `MAIL_CRYPTO_UNAVAILABLE`，绝不自动生成新种子覆盖退订哈希域。
- **五输出面硬约束**：
  1. **缓存/单飞**：邮件用户页、订阅页走私有 `Client` 路径（禁入 ETag、禁入 `AnalyticsCache`、禁入公共 `singleflight`）；明文邮箱仅存于解析/投递瞬态栈变量。
  2. **数据库**：终态/取消同事务置 `PayloadCipher = NULL`；退订表仅存 `EmailHash` + `EmailMasked`；`SiteOpsStore` 静默 logger；错误码只存枚举与数值 `SMTPCode`，绝不存原始 SMTP 文本。
  3. **日志**：`slog` 仅记录 `instance_id`、`batch_id`、`item_id`、`stage`、`error_code`、`smtp_code`、`latency_ms`、`counts`，零邮箱/密码/正文/远端原文。
  4. **API**：`SMTPView` 仅返回 `from_masked`、`has_username`、`has_password`，不返回明文账号/密码/发件邮箱；明细仅返回 `user_id`、`email_masked`、枚举状态与错码；全路径 `Cache-Control: no-store`。
  5. **通知/事件**：营销邮件与 `backend/notify/`、告警事件、运维日报零交互。

---

## 第 4 章：异步受众解析引擎与预算护栏

### 4.1 `Client` 低优先级与缓存策略解耦（`backend/siteops/client.go`）

在 `requestSpec` 中新增独立布尔字段 `lowPriority bool` 与 `bypassFlight bool`：
- 既有 P2 运营分析请求保持 `policy: cachePolicyAnalyticsLRU, lowPriority: true, bypassFlight: false`；
- 邮件私有目录请求使用 `policy: cachePolicyNoCache, lowPriority: true, bypassFlight: true`：
  - **绝不读写 `c.cache`（ETag）**，**绝不写入 `AnalyticsCache`**，**绝不跨批共享 `c.flights`**；
  - 仍然完整经过 `backoffLocked`（共享实例级 429 退避）、`acquire`（受 P2 低优先级并发子上限约束；若 `PerInstanceMaxConcurrency == 1` 则礼让 L1 排队或返回 `ErrBudgetExceeded`）、`reserveAudit(..., true)`（受单实例 L1 分钟/日预留与全局 P2 账本原子约束）与 `recordAdminLocked`（429 写回 `ExtendAdminBackoff`）。

### 4.2 五种受众模式与严格覆盖校验（`backend/siteops/mail_audience.go`）

- **统一硬上限**：单目录最多扫描 `100` 页 / `10,000` 行，单批唯一有效收件人上限 `2,000`，`preparing` 准备截止 `30` 分钟（按首次创建 `PrepareBy` 固定，重启从第 1 页重扫但保留原 `PrepareBy`）。
- **统一合法收件人基线**：`status == "active"` 且 `role ∈ {"user", "member"}`（永不向 `admin`、`disabled`、`banned`、`inactive` 或未知角色发营销信），通过 `NormalizeBareEmail` 校验，未命中本地 `site_ops_mail_suppressions`，批内按 `EmailHash` 去重（多 UID 同邮箱选最小 `UserID`）。
- **翻页漂移防线**：邮件专用投影保留原始 `page/page_size/total/has_more` 缺失标记；首末页 `total` 不一致、页间出现重复 ID、`Truncated == true` 或尾页缺总计且满页超限，一律中止 `preparing`、擦除已暂存 `PayloadCipher` 并落 `failed`（`MAIL_PREP_PAGE_DRIFT`）。
- **各模式精确口径**：
  1. `selected`：`1..500` 个去重正整数 UID（≤`2^53-1`），分页扫描用户目录，命中全部目标即早停；找不到/非活跃/非客户角色计 `excluded_count`。
  2. `groups`：`1..50` 个 `group_id` 并集；先全扫邮件私有订阅目录，以固定 `asOf = batch.CreatedAt` 按 `activeSub(s, asOf)` 提取有效订阅用户集合与主套餐信息，再扫用户目录求交集并去重。
  3. `segment`：支持 Wave 4 的 6 个标签（`high_value_at_risk`、`expiring_soon`、`expired_unrenewed`、`cache_anomaly`、`concurrency_queued`、`dormant_high_balance`）与 4 个活跃度（`drop`、`idle_balance`、`active`、`low`），同维 OR、跨维 AND（至少选一项）：
     - 先按 UID 建订阅索引（避免 O(N×M) 嵌套全扫），批量拉 `users-usage`（100/批）与 1 次 `user-concurrency`；
     - 复用既有 `userTags` 与 `userActivityState` 纯函数（传入 `Email: nil`）；
     - **预算与覆盖诚信原则**：若所选标签/活跃度依赖单用户 `usage/stats`（如 `cache_anomaly` 或 `high_value_at_risk` 骤降分支）且待评估人数导致所需调用数超过 `BudgetHeadroomForAnalytics` 或无法在 `PrepareBy` 内完成，或任意依赖域不可用，**直接中止并报错 `MAIL_PREP_COVERAGE_INCOMPLETE`**，严禁将缺失数据当作 `0` 误判为 `low`/`dormant`。
  4. `all`：全量合法 `active` `user/member`，超 2,000 唯一地址中止（`MAIL_PREP_LIMIT_EXCEEDED`）。
  5. `retry_failed`：仅由 `POST .../batches/:bid/retry-failed` 触发，读取源终态批中 `status=='failed'` 的 `(UserID, EmailHash)` 对；重扫当前目录，**必须同时匹配原 `UserID` 与原 `EmailHash`**，且仍为合法活跃客户、未退订；若客户已改邮箱则计 `excluded_count`，绝不向新地址发旧补发邮件。
- **干跑受众预览（`POST .../audience/preview`）**：受限同步扫描（最多 `5` 页、总超时 `5s`，绝不落库、走同一低优先级预算），返回估算有效数、跳过数与最多 `20` 条脱敏样本（`user_id`、`email_masked`、`plan_name`、`activity_state`、`tags`），若目录 `> 5` 页标 `is_estimate = true`。

---

## 第 5 章：模板引擎、安全 HTML 重建与 SMTP 投递引擎

### 5.1 9 项白名单变量与服务端安全 HTML 重建（`mail_html.go`、`mail_template.go`）

- **9 项只读白名单变量（`MailTemplateVars`）**：
  - 批次级静态变量（存于 `SnapshotJSON`）：`SiteName`（清洗后站名）、`SiteURL`（规范化 HTTPS 站址）、`TodayDate`（批次创建时 UTC+8 `YYYY-MM-DD`）；
  - 收件人级变量（仅当模板实际引用时按需密封进 `PayloadCipher`，终态同事务清除）：`UserID`（`U#1042`）、`EmailMasked`、`BalanceUSD`（`$28.50` / `—`）、`PlanName`（所选/首个有效活跃订阅组名或 `按量余额`）、`ExpiresDate`（同份订阅 UTC+8 到期日或 `—`）、`ExpiresInDays`（同份订阅向上取整剩余天数或 `—`）。
- **静态 AST 校验**：`Subject` 与 `TextBody` 用 `text/template`、`HTMLBody` 用 `html/template` 解析并遍历 `parse.Tree.Root`，仅允许 `NodeList`/`NodeText`/`NodeAction`/`NodeIf`，且管道内仅允许单个深度为 1 的白名单字段；严禁函数、管道、变量声明、`range`/`with`/`template`/`define`。
- **零依赖 HTML 白名单树重建器（`mail_html.go`）**：
  - 将模板 HTML 解析为安全节点树（保留 `{{...}}` 占位符文本节点，拒绝或剥离 `<script>`/`<style>`/`<iframe>`/`<form>`/`<object>`/`<img>` 及一切 `on*` 属性），仅保留营销卡片布局标签（`table/thead/tbody/tfoot/tr/td/th/div/span/p/br/hr/h1..h4/strong/b/em/i/u/ul/ol/li/a`）；
  - 仅允许白名单 HTML 属性（`style`、`align`、`valign`、`width`、`cellpadding`、`cellspacing`、`border`、`role`、`colspan`、`rowspan`，以及 `a` 的 `href`）；
  - `style` 属性按分号拆分 `prop: value`，仅放行安全排版/配色属性并严格校验值语法（禁止 `url(`、`@import`、`expression`、`javascript:`、`position:fixed` 等）；
  - `a[href]` 仅允许合法 `https://`、`mailto:` 或完整 `{{.SiteURL}}`，重建时自动补 `rel="noopener noreferrer"`；
  - 渲染后经由**同一节点树**派生：
    1. 正式邮件 `text/html`；
    2. 正式邮件 `text/plain`（若未自定义 `TextBody`，按块级换行 + 链接 `文本 (URL)` 生成可读纯文本）；
    3. 预览副本 `PreviewHTML`（将 `<a>` 重建为不可点击 `<span>` 防止 iframe 导航）。
- **3 套内置卡片模板**（每目标首次列表时幂等种入，可编辑、不可删除）：`builtin:marketing_promo`（活动通知卡片）、`builtin:renewal_reminder`（订阅临期续费提醒）、`builtin:reactivation`（余额休眠唤醒）。
- **MIME 组装**：标准库 `mime/multipart` + `encoding/base64`（76 列折行）+ 按 UTF-8 rune 边界切分的 RFC 2047 B 编码头，输出 `Date`、`Message-ID`（随机不含客户 ID）、`From`、`To`、`Reply-To`（可选）、`Subject`、`MIME-Version: 1.0`、`Auto-Submitted: auto-generated`、`Precedence: bulk`、可选 `List-Unsubscribe`（不含 `One-Click`）、`multipart/alternative`（`text/plain` 在前、`text/html` 在后），整封 MIME 硬上限 `256 KiB`。

### 5.2 SMTP 会话、限速与精准错误分流（`mail_smtp.go`、`mail_worker.go`）

- **连接与协议**：`OutboundGuard.DialSMTPContext`（5s 拨号超时）→ `implicit`（先 `tls.Client` + `HandshakeContext` 再 `smtp.NewClient`）或 `starttls`（`EHLO` → 强验 `Extension("STARTTLS")` → `StartTLS(tlsCfg)`）；均强制 `TLS 1.2+` 与证书链/主机名校验。认证支持 `none` / `plain` / `login` / `auto`（仅在已验证 TLS 上按升级后声明的机制协商；自研 `loginAuth` 校验 `server.TLS`、主机名与挑战顺序）。
- **连接复用与限速**：单批内每 worker 独占 1 条 SMTP 连接（仅同站同批同 `SMTPRevision` 复用，满 `ReconnectEvery` 封或闲置 30s 主动关闭重建；全进程全局最多 8 条活跃 SMTP 连接）；每站共享标准库单调时钟令牌桶（按 `RatePerMinute` 匀速发放，桶容量 `1`，等令牌后再 claim）；单封操作硬 deadline `30s`。信封拒收后可 `RSET` 复用，任何 `DATA` 阶段/网络/协议错误立即丢弃连接（写正文失败绝不调用 `writer.Close()` 以免发残缺邮件）。
- **精确证据矩阵**：
  - `config` / `render` / `guard` / `dial` / `tls` / `ehlo` / `auth` / `mail_from` / `rcpt_to` / `data_cmd`（未收到 `354`）以及 `data_commit` 收到明确 `*textproto.Error`（4xx/5xx）→ **`failed`**（确定未投递，允许一键新建补发子批）；
  - `data_commit` 收到 `250 OK` → **`sent`**（忽略 `QUIT` 错误；若落库提交失败仅重试落账绝不重发）；
  - 已持久化 `phase="data"` 后在写正文或等待最终回复期间断连/超时/EOF，或 `sending` 滞留 `>= 10m` 被回收 → **`uncertain`**（永久排除自动重试与失败补发）。
- **熔断机制**：同批遇到明确配置/证书/认证错误（`guard`/`tls`/`auth`）立即停止本批新领取，并将剩余 `pending` 置 `failed`（`MAIL_SMTP_CONFIG_BLOCKED`）+ 擦除密文；遇到连续 3 次网络拨号超时同样中止本批未发项。
- **测试发信特例**：`POST .../smtp/test` 以 `mode="test"` 复用批表、活跃槽与 `request_id` 持久幂等，且持久化校验同实例最近 60s 内未执行过测试（限频 1/min），仅建 1 条 `UserID=0` 发往已保存 `FromEmail` 的加密项（不出现在客户明细列表，不触发补发），HTTP 同步等待最多 20s，结束返回 `200`、未结束返回 `202`，统一返回 `MailTestResult`。

---

## 第 6 章：完整 REST API 契约（`backend/api/site_ops_mail.go`）

全部路由挂载于 `/api/site-ops/instances/:id/mail`，所有响应带 `Cache-Control: no-store` 与 `X-SiteOps-Mail-Auth-Mode`；写请求限体（常规 16 KiB，模板 64 KiB）、禁止未知字段、禁止重复 JSON 键、禁止尾随 token、正整数上限 `2^53-1`；错误体统一为 `{"code","error","field?","active_batch_id?","retry_after_seconds?"}`。

| 方法 | 相对路径 | Query | 成功码 | 核心响应 DTO 与契约 |
| :--- | :--- | :--- | :---: | :--- |
| `GET` | `/overview` | — | `200` | `MailOverview`（`security_mode`, `generation`, `cred_gen`, `smtp_ready`, `smtp_from_masked`, `template_count`, `suppression_count`, `active_batch`, `copyable_sources`） |
| `GET` | `/smtp` | — | `200` | `MailSMTPView`（无明文密码/账号/发件邮箱，含 `revision`, `from_masked`, `has_username`, `has_password`） |
| `PUT` | `/smtp` | — | `200` | `SaveMailSMTPInput`（带 `generation`, `cred_gen`, `expected_revision` CAS）→ `MailSMTPView` |
| `POST` | `/smtp/test` | — | `200/202` | `MailTestInput`（`generation`, `cred_gen`, `expected_revision`, `request_id`）→ `MailTestResult`（仅发已存 From） |
| `POST` | `/smtp/copy` | — | `200` | `MailCopyInput`（含源/目标 `generation`, `cred_gen`, `revision`）→ `MailSMTPView` |
| `GET` | `/templates` | `cursor`, `limit`(≤100) | `200` | `MailCursorPage[MailTemplateSummary]`（自动确保 3 套内置模板；**不含 `html_body`**） |
| `GET` | `/templates/:tid` | — | `200` | `MailTemplateView`（完整详情含 `html_body`, `text_body`, `revision`） |
| `POST` | `/templates` | — | `201` | `SaveMailTemplateInput`（`expected_revision=0`）→ `MailTemplateView` |
| `PUT` | `/templates/:tid` | — | `200` | `SaveMailTemplateInput`（带 `expected_revision`）→ `MailTemplateView` |
| `DELETE` | `/templates/:tid` | `generation`, `cred_gen`, `expected_revision` | `200` | `MailDeletedView`（内置模板拒删返 400） |
| `POST` | `/templates/copy` | — | `201` | `MailCopyInput` + `source_template_id` → 新 `MailTemplateView` |
| `POST` | `/templates/preview` | — | `200` | `MailTemplatePreviewInput` → `MailTemplatePreview`（服务端安全重建的 `subject`, `preview_html`, `text`, `used_vars`） |
| `GET` | `/candidates` | `page`(1..100) | `200` | `MailCandidatePage`（仅 `user_id` + `email_masked`，走私有低优先级投影） |
| `GET` | `/groups` | — | `200` | `[]MailGroupOption`（`id`, `name`，复用安全分组元数据） |
| `POST` | `/audience/preview` | — | `200` | `MailAudiencePreviewInput` → `MailAudiencePreview`（≤5 页 / 5s 干跑估算 + ≤20 脱敏样本） |
| `POST` | `/batches` | — | `202/200` | `CreateMailBatchInput`（`request_id`, `generation`, `cred_gen`, `smtp_revision`, `template_id`, `template_revision`, `audience`）→ `MailBatchView` |
| `GET` | `/batches` | `cursor`, `limit`(≤50), `status` | `200` | `MailCursorPage[MailBatchView]`（不含 `test` 批、不含大 HTML） |
| `GET` | `/batches/:bid` | — | `200` | `MailBatchView`（实时进度、四态计数、`can_cancel`, `can_retry_failed`） |
| `GET` | `/batches/:bid/items` | `cursor`, `limit`(≤100), `status` | `200` | `MailCursorPage[MailItemView]`（`user_id`, `email_masked`, `status`, `phase`, `smtp_stage`, `smtp_code`, `error_code`, `hard_bounce_hint`） |
| `POST` | `/batches/:bid/cancel` | — | `200` | `MailScopeInput` → `MailBatchView`（幂等取消；已正常封账返 409） |
| `POST` | `/batches/:bid/retry-failed` | — | `202/200` | `RetryMailBatchInput`（`generation`, `cred_gen`, `request_id`, `smtp_revision`）→ 子 `MailBatchView` |
| `GET` | `/suppressions` | `cursor`, `limit`(≤100) | `200` | `MailCursorPage[MailSuppressionView]`（仅 `id`, `email_masked`, `reason`, `created_at`） |
| `POST` | `/suppressions` | — | `201/200` | `AddMailSuppressionInput`（支持 `emails` 1..50 或 `item_id` 直接复用其 `EmailHash`）→ `MailSuppressionAddResult` |
| `DELETE` | `/suppressions/:sid` | `generation`, `cred_gen` | `200` | `MailDeletedView` |

---

## 第 7 章：前端 `/site-ops` 第 6 子模块与 Tab 1 联动规范

1. **接入点（仅改 3 个既有前端文件）**：
   - `frontend/app/site-ops-page.tsx`：`DetailSection` 增加 `"mail"`；新增第 6 个 Tab `<TabsTrigger value="mail">营销邮件</TabsTrigger>`；新增一次性状态 `mailDraft: SiteOpsMailDraft | null`（切站/删站/登出清零）；仅在 `selected && activeSection === "mail" && !blocked` 时条件挂载 `<MailPanel key={`${selected.id}:${selected.generation}:${selected.cred_gen}`} ... />`。
   - `frontend/components/site-ops/analytics-panel.tsx`：向 `AnalyticsUsersTable` 透传可选 `onComposeMail`。
   - `frontend/components/site-ops/analytics-users-table.tsx`：增加轻量行勾选框、筛选栏「给当前页已选/命中客户发邮件」按钮与单行「发邮件」动作；**回调仅传递正整数 `userIds: number[]` 与诚实文案 `sourceLabel`（如 `"当前页已勾选 5 人"` / `"当前页筛选命中 12 人"`），绝不传递整行数据或邮箱**。
2. **自治子模块组件（全部位于 `frontend/components/site-ops/`）**：
   - `mail-panel.tsx`：入口壳，首次只拉 `GET /overview`；处理 `403 SITE_OPS_MAIL_AUTH_REQUIRED` 仅在面板内显示提示卡（**不触发全局 `onAuthRequired`**）；顶栏展示安全模式、SMTP 就绪灯、活跃批入口、`[SMTP 设置]`、`[退订名单]` 弹窗按钮；切站/换代/内部切视图前保护未保存脏草稿；严格使用 `ref` 防 StrictMode 双消费 `mailDraft` 且按匹配 `nonce` 函数式清草稿。
   - `mail-compose.tsx`：受众选择（`selected` / `groups` / `segment` / `all`）+ 干跑受众预览 + 模板选择 + 服务端沙箱预览 + 幂等确认弹窗（弹窗生命周期绑定同一个 `request_id`，失败重发不重复建批）。
   - `mail-batches.tsx`：游标分页批次列表 + 详情抽屉/面板；仅当活跃批详情可见时采用串行 `setTimeout` 3s 轮询（429 遵 `retry_after_seconds`，5xx/网络异常指数退避至 30s），隐藏/离开/终态立即清 timer 并 `abort`；明确失败项支持「一键补发确定失败项」并显式说明排除 `uncertain`。
   - `mail-templates.tsx`：左栏模板摘要列表（内置标识 + 自定义配额 X/50 + 新建 + 从其他站点复制）+ 右栏编辑器（字节计数 ≤24 KiB + 9 变量一键插入）+ 服务端安全预览 `<iframe sandbox="" referrerPolicy="no-referrer">`（固定 CSP `default-src 'none'; style-src 'unsafe-inline'; img-src 'none'; font-src 'none'; script-src 'none'; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'`）。
   - `mail-smtp-dialog.tsx`：本站 SMTP 配置、改主机/账号强制重填密码、从其他站点复制、发送测试信（发往已保存 From）。
   - `mail-suppression-dialog.tsx`：退订名单游标分页、批量添加与移除确认。

---

## 第 8 章：多 Git Worktree 并行施工拓扑与验收 DoD

### 8.1 精确文件所有权（零重叠，每波最多 2 路并行适配 ~1.5 GiB 内存）

所有路径根为 `D:\AI\upstream-ops`。严禁修改 `go.mod`、`go.sum`、`frontend/package.json`、`frontend/pnpm-lock.yaml`。

| 波次 / 分支 | 写者 | 独占文件所有权 | 独立验收命令 |
| :--- | :---: | :--- | :--- |
| **Wave 0**<br>`feat/w5-contracts` | 单写者 | `backend/storage/site_ops_mail_model.go`、`backend/siteops/types_mail.go`、`frontend/lib/site-ops-mail-types.ts` | `go build ./backend/storage ./backend/siteops` + `pnpm --dir frontend exec tsc --noEmit` |
| **Wave 1A**<br>`feat/w5-storage` | 并行写者 1 | `backend/storage/storage.go`、`backend/storage/site_ops.go`、`backend/storage/site_ops_mail.go`、`backend/storage/site_ops_mail_test.go`、`backend/siteops/store_mail.go`、`backend/config/config.go`、`backend/config/config_mail_test.go`、`backend/scheduler/scheduler.go`、`backend/scheduler/site_ops_mail_test.go` | `go test -p 1 -count=1 -timeout 60s ./backend/storage ./backend/config ./backend/scheduler ./backend/siteops` |
| **Wave 1B**<br>`feat/w5-engine` | 并行写者 2 | `backend/siteops/guard.go`、`backend/siteops/client.go`、`backend/siteops/client_analytics.go`、`backend/siteops/client_mail.go`、`backend/siteops/mail_crypto.go`、`backend/siteops/mail_crypto_test.go`、`backend/siteops/mail_html.go`、`backend/siteops/mail_template.go`、`backend/siteops/mail_template_test.go`、`backend/siteops/mail_smtp.go`、`backend/siteops/mail_smtp_test.go`、`backend/siteops/mail_audience.go`、`backend/siteops/mail_audience_test.go` | `go test -p 1 -count=1 -timeout 60s ./backend/siteops` |
| **Wave 2A**<br>`feat/w5-backend-api` | 并行写者 3 | `backend/siteops/service.go`、`backend/siteops/wire.go`、`backend/siteops/mail_access.go`、`backend/siteops/mail_access_test.go`、`backend/siteops/mail_service.go`、`backend/siteops/mail_worker.go`、`backend/siteops/mail_worker_test.go`、`backend/siteops/service_mail_test.go`、`backend/api/api.go`、`backend/api/site_ops.go`、`backend/api/site_ops_mail.go`、`backend/api/site_ops_mail_test.go` | `go test -p 1 -count=1 -timeout 90s ./backend/storage ./backend/siteops ./backend/api ./backend/scheduler ./backend/config` |
| **Wave 2B**<br>`feat/w5-frontend` | 并行写者 4 | `frontend/lib/site-ops-mail-api.ts`、`frontend/lib/site-ops-mail-state.ts`、`frontend/lib/site-ops-mail-state.test.ts`、`frontend/app/site-ops-page.tsx`、`frontend/components/site-ops/analytics-panel.tsx`、`frontend/components/site-ops/analytics-users-table.tsx`、`frontend/components/site-ops/mail-panel.tsx`、`frontend/components/site-ops/mail-compose.tsx`、`frontend/components/site-ops/mail-batches.tsx`、`frontend/components/site-ops/mail-templates.tsx`、`frontend/components/site-ops/mail-smtp-dialog.tsx`、`frontend/components/site-ops/mail-suppression-dialog.tsx` | `pnpm --dir frontend exec tsc --noEmit --incremental false && pnpm --dir frontend run lint && pnpm --dir frontend run build` |

### 8.2 必过验证与部署 DoD

1. **后端全量单测**（不设 `GOTOOLCHAIN`，串行 `-p 1` 控内存）：
   `go test -p 1 -count=1 -timeout 90s ./backend/storage ./backend/config ./backend/scheduler ./backend/siteops ./backend/api ./backend/runtimeconfig`
2. **关键安全与隐私断言必测**：
   - 五面隐私金丝雀测试（合成哨兵邮箱、SMTP 密码、自定义报错正文在全库、slog 捕获缓冲、API 响应、内存缓存、通知通道均为零泄露，所有终态 `PayloadCipher IS NULL`）；
   - 五重本机回环门禁测试（`authSubject` 放行、直接回环放行、非回环/转发头/公网 Host/跨站 Origin/非 JSON 写全部 403、含 401/403 响应必带 `Cache-Control: no-store`）；
   - `ActiveSlot` 并发建批互斥、两阶段 DATA 授权取消/退订拦截、SMTP 证据分类矩阵（`sent`/`failed`/`uncertain`）、10m 滞留回收、系统性故障熔断、`Service.Stop()` 无死锁宽限退出；
   - `go.mod`、`go.sum`、`frontend/package.json`、`frontend/pnpm-lock.yaml` `git diff` 均为空。
3. **前端验证**：
   `pnpm --dir frontend exec tsc --noEmit --incremental false`、`pnpm --dir frontend run lint`、`node --experimental-strip-types --test frontend/lib/site-ops-mail-state.test.ts`（或既有前端测试命令）及 `pnpm --dir frontend run build`。
4. **本地编译与上线验证**（严禁 `git push`）：
   - 停止旧 `:8418` 进程 → `$env:CGO_ENABLED='0'; go build -o D:\AI\upstream-ops\upstream-ops.exe ./cmd/server` → 静默加载 `APP_SECRET` 启动并验证 `/healthz` + `http server listening`；
   - 验证 `:8418` 与 `:3010` 代理访问 `/api/site-ops/instances/:id/mail/overview`、`templates`、`batches`、`suppressions` 返回 `200` + `Cache-Control: no-store` + `X-SiteOps-Mail-Auth-Mode: local_loopback_only`，且既有 `/site-ops` 5 大 Tab 与 `/embed` 零回归。
