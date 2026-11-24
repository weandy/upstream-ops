# AGENTS.md

协作方式见 `C:\Users\123\.agents\AGENTS.md`。本文件只记这个仓库里代码看不出来的事实。当天的运行现象写到 `docs/plans/`，不要继续堆在这里。

用户可见的说明、计划和提交说明用中文。不要把密钥、Cookie、`APP_SECRET` 或数据库内容写进仓库、提交或这份文件。

## 仓库

- 本地路径：`D:\AI\upstream-ops`。旧路径 `E:\ai\jz` 已作废。分支：`main`。
- 这是上游仓库的二次开发。发布镜像记录在 `weandy/upstream-ops`。**不要 push 到 `bejix/upstream-ops`。用户没有明确说 push 时，也不要 push 自己的远程。**
- 不要提交：`config.yaml`、`data/`、`*.exe`、`.vite/`、`frontend/node_modules/`、`web/dist/` 里的构建产物、`.env`。`config.yaml` 和 `data/` 已被 gitignore，不要删。
- 代码和文档里不要出现 `ooioo`、`bothyouandme`。对外示例用 `portal` / `portal.example.com`。

## 不要改回去的行为

- 测活默认 60 秒。`candy`、`lmfpd`、`pelican`、`custom_prompt` 默认 3600 秒。调度 tick 5 秒。调度按锚点加间隔，不是整点。
- 可用率条固定 60 格。一格的长度等于该目标的测活间隔：1 分钟一测时整条约 60 分钟，5 分钟一测时整条约 300 分钟。文案用「近 N 分钟」，不要写死 60，也不要改回 90 分钟或 3 分钟一格。
- 热力条保持紧凑、单行、等高胶囊。不要恢复高低差矮柱，不要在每行重复「60 分钟前 / 现在」，不要做成占半屏的大盒子。
- 不要改 Sub2API 登录，不要重做凭证加密。
- 收入、消耗、大客户与用户缓存率分析仅限自有站点独立模块 `siteops`（`backend/siteops/`）；既有外部上游监控模块依然不涉及 `/api/log/stat` 或外部站销售分析。`siteops` 对缓存、数据库、日志、API、通知五个输出面必须执行字段级隐私白名单。
- 不要改流必须有结束标记的判断，也不要改 2MiB 捕获上限，除非用户再次要求。结束标记是 `[DONE]`、`finish_reason` 为 `stop`/`end_turn`、`response.completed` 或 `message_stop`。
- `reasoning_effort` 可以省略。不要再做一套自定义题目占位符；`custom_prompt` 已经存在。

## 本地怎么跑

- 后端 `http://localhost:8418`，二进制是仓库根目录的 `upstream-ops.exe`。公开页：`http://localhost:8418/embed?site=default`。鹈鹕画廊：`http://localhost:8418/embed/pelican?site=default&target=codex`。
- 管理台开发地址是 `http://localhost:3010/`。Vite 只绑 IPv6 的 localhost，`http://127.0.0.1:3010/` 会连接失败。代理目标在 `frontend/vite.config.ts`。
- 启动后端前，把 `config.yaml` 里 `security.appSecret` 读进环境变量 `APP_SECRET`。不要打印这个值。
- 本机 Go 是 1.26.1，已满足 `go.mod` 的 `go 1.23`。不要再设 `GOTOOLCHAIN=go1.23.0`。编译：`$env:CGO_ENABLED='0'; go build -o D:\AI\upstream-ops\upstream-ops.exe ./cmd/server`。
- Windows 上 exe 被占用时，先结束监听 8418 的进程再编译。用能留住后台进程的方式启动，并确认日志里有 `http server listening`。
- 改 Go（包括 `backend/embed/templates.go`）后必须重新编译并重启。Vite 热更新看不到服务端渲染。exe 里嵌的是 `web/dist`；只改前端源码时，用 3010 看新界面，不要误以为 8418 里的管理台已经更新。
- `auth.enabled` 默认关闭，密码为空。`/api/auth/me` 只有 401 才进登录页；网络失败或 5xx 应显示后端没连上。
- 界面时间是 UTC+8，库里的时间是 UTC。重启会打断正在跑的探测，但不要删库。

## 已经落地的行为

### New API 会话与密钥分页

提交 `13c2a15`。设计说明在 [docs/design-newapi-upstream-session.md](docs/design-newapi-upstream-session.md)。

- 业务请求遇到 401 时，按渠道或 refresh token 单飞刷新一次；30 秒内复用刚刷新的会话。只有密码模式才把新会话存回去。
- `nap_` 只走 Bearer。旧令牌没有用户 ID、也没有 Cookie 时，提示需要填写用户 ID。`require_verification` 按双因素处理。
- 找密钥时分页，每页 100，最多 100 页。管理端列表接口本身仍分页。

这次提交同时改了概览接口，让它按测活间隔回看；当时 `AggregateLiveness` 还是固定 1 分钟，所以那一次提交自己编不过。下面的可用率提交把计算补齐。

### 可用率条

- `AggregateLiveness(samples, now, interval)` 按测活间隔铺 60 格。间隔短于最小允许值时用默认 60 秒。
- `TrendWindowMinutes` 给「近 N 分钟」文案。`TrendLookback` 多取一格；当前格还没有采样时，右端对齐到上一格，避免最右边固定空一格。
- 24 小时性能汇总只统计真实采样。空格表示这个时间窗里没有采样。
- 管理台、历史抽屉和公开页都读 `trend_window_minutes`。历史抽屉用请求序号丢掉过期响应，避免切换目标时闪出上一个目标的记录。格子悬停用 portal；间隔长于 1 分钟时显示这一格的起止时间。
- 公开页列宽在 `backend/embed/templates.go` 的 `.col-trend`。管理台条在 `frontend/app/intelligence-page.tsx`，当前是单行 `flex-1`。

### 画鹈鹕

- 内置名是 `鹈鹕原版html`。正文常量是 `pelicanPromptOriginal`：不使用工具，回复一个完整 HTML 文件，内联 SVG 动画，放在 ` ```html ` 代码块里。
- 插件配置里可以改当前题目，或「另存」成这个目标自己的版本。数据只在该目标的 `params_json`：`version`、`prompt`、`versions`。最多 20 个版本，题目最多 8000 个字符。不新建表，不跟其他目标共享。留空或选内置名时用常量。内置原文不要改掉。
- 库存的 `variant=original` 和 `variant=all-in-one` 仍能通过校验，并且都走这篇原文。`all-in-one` 的纯 SVG 题目已经删除。
- 默认同步超时在 README 里仍是 180 秒，`max_tokens` 默认 4096。单个目标可以自行把超时调大。完整画面超过 256KiB 不保存。
- 定时缓存只复用**其他目标**的**原始采样**（`reused_from_run_id` 为空，且目标 ID 不同）。本目标到点必须重新请求。手动运行不走这份缓存，也不改 `next_run_at`。测活不使用这份缓存。画廊不展示副本行。不要为了去重删除历史副本。
- 画面里的「暂停动画」是模型写进 HTML 的，不是提示词或嵌入页加的。同一次提示词，有的结果有按钮，有的没有。

### 调度

`ComputeNextSchedule` 在完成时刻仍落在当前锚点的一个间隔内时，保持原来的锚点。例如间隔 1 小时、12:49 左右结束，下一轮靠近 13:49，而不是 13:00。手动测一次不会把自动档改到刚才。

## 运行现象

2026-11-22 的测活和画鹈鹕耗时记在 [docs/plans/2026-11-22-upstream-runtime.md](docs/plans/2026-11-22-upstream-runtime.md)。那是当时库里的结果，不是待改代码。不要据此去改提示词或结束标记判断，也不要把目标密钥写进提交或文档。

## 验证

- Go 测试按改动范围跑，例如 `go test ./backend/intelligence/ ./backend/embed/`。不要设 `GOTOOLCHAIN=go1.23.0`。
- 前端 ESLint 要在 `frontend/` 目录里跑。仓库根目录没有 ESLint 配置，在根目录跑会报找不到配置。

## 接手时不要做

- 不要 push，除非用户明确要求。
- 不要把 `.vite/`、exe、数据库、`config.yaml` 加进提交。
- 不要把可用率改动和鹈鹕改题混进同一次无关提交；这两件事已经分开。
- 不要为了画廊去重删除历史运行记录。
