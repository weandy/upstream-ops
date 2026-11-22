# New API 上游对接：登录续期与 Key 翻页

只改和上游站点对接有关的两件事。不改营收、用量统计、销售、大户，也不改 Sub2API。

参考实现：[Gwen-Liu/transit-hub](https://github.com/Gwen-Liu/transit-hub) 的 `backend/internal/modules/upstream/newapi_auth.go`，以及同一批提交里「上游 Key 只读第一页」的修法。对照的是我们自己的 `backend/connector/newapi/newapi.go` 和 `backend/channel/service.go`。

## 现状

账号密码登录、短期 JWT、`new_api_refresh` Cookie 续期，主路径已经有了：

- 登录读 `data.access_token`，刷新令牌从 `Set-Cookie: new_api_refresh` 取出。
- `POST /api/user/auth/refresh` 带这个 Cookie，成功后换上新的 access token 和新 Cookie。
- 刷新返回 401 时，退回账号密码重新登录。
- 会话离过期不足 5 分钟就先刷新。并发刷新已有测试，不能拿旧刷新令牌打两次。

还缺的是 Liu 那批里仍会让对接失败的部分。

## 1. 登录续期还要补的

### 1.1 业务请求遇到 401，刷新一次再重试

现在 `getJSON` / `getRaw` 收到 401 就直接失败。过期判断只发生在发请求之前。上游时钟偏差、或者 15 分钟的 JWT 刚好在请求途中失效时，这一次余额、倍率、Key 查询会失败，要等下一次调度才恢复。

做法：

- 仅 New API、且当前会话带有 `RefreshToken` 时，401 之后调用现有 `RefreshSession`，把新会话写回渠道，然后用新会话把原请求重放一次。
- 重放仍是 401，就停止，交给现有的「刷新失败再重新登录」。不要在同一次请求里登录两次。
- 同一个渠道的刷新必须串行。并发的 401 只能有一个去打 `/api/user/auth/refresh`，其余等待这一次的结果。刷新令牌会被上游轮换，重放旧 Cookie 会把刚换到的会话作废。
- Cookie 模式（没有刷新令牌）和纯令牌模式不走这条。它们没有可轮换的 refresh cookie。

### 1.2 个人访问令牌 `nap_`

新版 New API 的长期自动化凭据是个人访问令牌，前缀 `nap_`。旧的 32 位系统访问令牌升级后会失效。我们的令牌模式现在只是把用户填的字符串放进 `Authorization`，并且仍要求填写用户 ID。

做法：

- 令牌以 `nap_` 开头时，按 Bearer 发送，不走 Cookie 登录，也不做刷新。
- 用户 ID 留空时，用这个令牌请求 `GET /api/user/self`，从返回里取出用户 ID，写入会话后再发后续请求。
- 旧令牌缺用户 ID、或上游拒绝时，错误写成「旧版令牌需要填写用户 ID」，不要报成笼统的登录失败。
- 开启了两步验证、登录停在 `require_verification` 或 `require_2fa` 时，明确说这个账号开了验证，监测账号需要关掉，或改用个人访问令牌。不要再落到「响应里没有用户 ID」。

账号密码登录的主路径不改协议，也不保存明文密码以外的新字段。`nap_` 仍放在现有令牌凭据的 `access_token` 里。

## 2. Key 翻页

`ListAPIKeys` 已经能按 `p` 和 `page_size` 要一页，分组汇总 `ListAPIKeyGroupSet` 也会一直翻到最后一页。会漏 Key 的是只看第一页、最多 100 条的几处：

- 网关给路由找或创建上游 Key：`backend/gateway/admin_routes.go` 的 `ensureSourceAPIKey`。搜索和全量回退都停在第 1 页。
- 智力监测按渠道找测活用 Key：`backend/intelligence/service.go` 同样只看第 1 页。
- New API 创建 Key 之后按名字或新 ID 回查：`backend/connector/newapi/newapi.go` 的 `CreateAPIKey` 只扫第 1 页 100 条。Key 超过 100 个、新 Key 又不在这一页时，会报缺少 Key ID，尽管上游已经创建成功。

做法：

- 在 connector 增加一个只给「按名字或 ID 找 Key」用的翻页函数。每页 100 条，沿用现有的页数上限，防止 `pages` 异常时死循环。
- 某一页返回条数小于页大小，或页码已经到达 `pages`，就停止。
- 网关和智力监测先按名字搜索。搜索接口如果也分页，同样翻完。搜索没有命中，再翻全量列表，匹配稳定名、已记录的 Key ID、旧名。
- 创建 Key 的回查使用同一个翻页函数，不再写死第 1 页。
- 管理台密钥列表接口保持按页返回，不在一次请求里把上游全部 Key 拉回浏览器。

Sub2API 的列表调用点如果共用这个查找函数，行为只是「找不到时继续下一页」。不改 Sub2API 的登录。

## 不做

- 不改 `/api/log/stat`，不改营收、分组用量、销售统计。
- 不引入凭据加密方案的改造。现有渠道密码加密保持不变。
- 不改测活题目、可用率条、推理挡位。
- 不自动关闭上游的两步验证，也不代用户完成验证码以外的二次验证。

## 验收

- 模拟 access token 过期：第一次业务请求 401，刷新成功后同一次操作拿到数据，并且渠道里保存的是轮换后的 refresh cookie。
- 两个并发请求同时 401：刷新接口只被调用一次。
- 令牌填 `nap_`、用户 ID 留空：能从 `/api/user/self` 识别用户并完成一次余额或 Key 查询。
- 登录响应为 `require_verification: true`：返回明确错误，不写入半套会话。
- 上游有 101 个 Key，目标 Key 在第 2 页：网关绑定、监测目标找 Key、创建后回查都能找到，且不会再建一把同名 Key。
