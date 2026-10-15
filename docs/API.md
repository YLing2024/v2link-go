# v2link-go 控制面 API（权威契约）

本文件是 v2link-go 控制面 HTTP 接口的权威契约。**改了路由不改本文件 = 未完成**：
路由在 `internal/httpapi/routes.go` 的 `routeTable` 里以 `{pattern: "METHOD /path", ...}` 字面量登记，
`scripts/check-api-docs.mjs` 会机检「源码登记 ↔ 本文 `### METHOD /path` 标题」一致，并随 `make check` 一起跑。

接口行为逐条对齐旧实现（`/root/proj/v2link`，Node 24 + Express 4）。差异见 §6「有意差异」。

- 统一前缀：`/api`；默认监听 `127.0.0.1:19245`。
- 请求 / 响应体一律 UTF-8 JSON。响应头 `Content-Type: application/json; charset=utf-8`。
- 成功信封 `{"ok":true,"data":…}`；失败信封 `{"ok":false,"error":"<中文消息>"}`（业务鉴权 401 例外，见 §3）。
- 示例统一用 `127.0.0.1` / `example.com`；下文的 `$B` 表示控制面基址，`$C` 表示登录后的 cookie 文件。

## 1. 认证模式

由环境变量 `AUTH_MODE` 决定（默认 `builtin`，非法值回落 `builtin`）：

| 模式 | 身份来源 | 说明 |
|---|---|---|
| `builtin`（默认） | 会话 Cookie `v2link_session` 或 `Authorization: Bearer <token>` | 口令用 scrypt 校验；登录下发会话 Cookie。**忽略** `X-Auth-User`。 |
| `sso` | 请求头 `X-Auth-User` | 只信任前置网关注入的用户名；`/api/auth/login\|logout\|me` 一律 404。 |

- 会话有效期 `SESSION_TTL_HOURS`（默认 12 小时，命中滑动续期）。
- 登录失败限流：每 IP 15 分钟窗口内失败超过 10 次后返回 429（带 `retryAfter` 秒数）。
- 免鉴权路径：`/api/healthz`、`/api/auth-mode`、`GET /api/clash/subscriptions/:token`。
- 两种模式下缺失凭证一律 401（不 302、不 500）。

## 2. 数据模型约定

### LinkView（`/api/links` 列表与单条响应）

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 主键，形如 `lk_` + 8 位（本服务内唯一）。**字符串，禁止按数字处理**。 |
| `uuid` | string | VLESS 客户端 UUID（RFC 4122 v4）。 |
| `note` | string | 备注（最长 500 字符）。 |
| `alias` | string | 节点别名（最长 100 字符），作为 vless URI 的 `#fragment`。 |
| `upBytes` / `downBytes` | number | 累计上行 / 下行字节。 |
| `createdAt` / `expiresAt` | number | epoch 毫秒。`expiresAt=0` 是永久哨兵。 |
| `permanent` | boolean | `expiresAt===0` 的派生布尔。 |
| `revokedAt` | number \| null | 吊销时刻（epoch 毫秒）。 |
| `status` | `active` \| `expired` \| `revoked` | 状态机见 §5。 |

### 其他

- 时间统一为 epoch 毫秒整数（旧实现 `users`/`sessions` 的 ISO 文本在此重设计中统一为整数，见 `docs/DB.md`）。
- 分页信封：`{"rows":[…],"total":<number>,"limit":<number>,"offset":<number>}`。
- 空集合序列化为 `[]`（不是 `null`）。

## 3. 鉴权失败与兜底

- 业务 API（`/api/links*`、`/api/audit`、`/api/regions/*`、`POST /api/clash/subscriptions`）未认证：
  `401`，体为 **只有 error、没有 ok** 的 `{"error":"未登录"}`（对齐旧中间件）。
- 未知 `/api/*` 路径先过业务鉴权：未认证 → `401 {"error":"未登录"}`；已认证 → `404 {"ok":false,"error":"接口不存在: <METHOD> <path>"}`。
  （与旧实现同源：Express 里 `/api` 业务鉴权中间件挂在业务路由之前。）
- 非 `/api` 路径的未知路由（非 GET/HEAD）→ `404`，体为 `Not Found`（`text/html; charset=utf-8`）。
- 请求体不是合法 JSON → `500 {"ok":false,"error":"<解析消息>"}`（旧实现走统一错误处理）。

---

## 4. 端点

### GET /api/healthz

健康检查，**免鉴权**。只报告进程存活与数据面内存快照，**不做 DB / xray 往返**。

- 请求参数：无。
- 副作用：无。
- 响应 `200`：

```json
{"ok":true,"data":{"status":"ok","now":1792079030162,"uptime":210090,"xray":{"activeLinks":2,"reconciledAt":1792079004856,"lastFix":{"added":0,"removed":0}}}}
```

```bash
curl -sS "$B/api/healthz"
```

### GET /api/auth-mode

返回当前认证模式，**免鉴权**（前端据此决定显不显示登录页）。

- 响应 `200`：`{"authMode":"builtin"}`（或 `"sso"`）。
- 副作用：无。

```bash
curl -sS "$B/api/auth-mode"
```

### POST /api/auth/login

`builtin` 模式下的登录；校验口令 → 建会话 + 写 HttpOnly Cookie。**`sso` 模式返回 404**。

- Body：`username`（string，必填）、`password`（string，必填）。
- 限流：每 IP 15 分钟窗口内失败 > 10 次 → `429`，体 `{"ok":false,"error":"尝试过于频繁，请稍后再试","retryAfter":<秒>}`。
- 成功 `200`：`{"ok":true,"user":{"name":"admin"}}`，并设 `Set-Cookie: v2link_session=<token>; Max-Age=…; Path=/; Expires=…; HttpOnly[; Secure]; SameSite=Lax`。
- 失败 `401`：`{"ok":false,"error":"账号或密码错误"}`。
- 副作用：写 `sessions` 表；失败计数进内存限流器。

```bash
curl -sS -c "$C" -X POST -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<口令>"}' "$B/api/auth/login"
```

### POST /api/auth/logout

删除当前会话并清 Cookie（幂等）。**`sso` 模式返回 404**。

- 响应 `200`：`{"ok":true}`，并清空 `v2link_session` Cookie。
- 副作用：删 `sessions` 行。

```bash
curl -sS -b "$C" -X POST "$B/api/auth/logout"
```

### GET /api/auth/me

返回当前登录用户名。**`sso` 模式返回 404**。

- 成功 `200`：`{"ok":true,"name":"admin"}`。
- 未登录 `401`：`{"ok":false,"error":"未登录"}`。

```bash
curl -sS -b "$C" "$B/api/auth/me"
```

### GET /api/links

返回全量账本展示形态（`upBytes` 等派生字段），按创建时间倒序。**需鉴权**。

- 响应 `200`：`{"ok":true,"data":[<LinkView>, …]}`。
- 副作用：无。

```bash
curl -sS -b "$C" "$B/api/links"
```

### POST /api/links

生成一条新链接并热加到数据面（`xray api adu`）。**需鉴权**。

- Body：
  | 字段 | 类型 | 必填 | 默认 | 校验 |
  |---|---|---|---|---|
  | `note` | string | 否 | `""` | trim 后按 500 字符截断 |
  | `alias` | string | 否 | `""` | trim 后按 100 字符截断 |
  | `hours` | number | 否 | `DEFAULT_HOURS`(24) | 整数，1~`MAX_HOURS`(720) |
  | `expiresAt` | number | 否 | — | epoch 毫秒整数、晚于当前、且在未来 365 天内 |
  | `permanent` | boolean | 否 | `false` | 布尔 |

  `hours` / `expiresAt` / `permanent` **最多指定一个**（同时传 ≥2 个 → 400）。
- 过期时刻优先级：`permanent`(哨兵 0) > `expiresAt` > `hours`。
- 成功 `201`：`{"ok":true,"data":<LinkView>}`。
- 错误：
  - `400` 入参非法（如 `时长须为 1~720 小时的整数`、`expiresAt 须晚于当前时间（不允许设置过去时刻）`）。
  - `502` `xray 添加用户失败，已回滚: <原因>`（本次创建未生效：账本行与审计行同事务撤销）。
- 副作用：写 `links` + `audit_log`（action=`create`）；调 `xray api adu`。

```bash
curl -sS -b "$C" -X POST -H 'Content-Type: application/json' \
  -d '{"hours":24,"note":"验收样例","alias":"节点一"}' "$B/api/links"
```

### POST /api/links/:id/revoke

吊销链接：先标 `revoked` + 审计（事务），再 `rmu`。**仅 `active` 可操作**。**需鉴权**。

- Path：`id` 为链接 id。
- 成功 `200`：`{"ok":true,"data":<LinkView>}`（`status=revoked`）。
- 错误：`404 链接不存在`；`400 仅 active 链接可操作（当前 <status>）`；`502 xray 删除用户失败，已回滚: <原因>`。
- 副作用：改 `links.status`/`revoked_at`；写审计（action=`revoke`）；调 `rmu`。

```bash
curl -sS -b "$C" -X POST "$B/api/links/<id>/revoke"
```

### POST /api/links/:id/extend

延长 / 转换有效期（仅控制面字段，不触 xray）。**仅 `active` 可操作**。**需鉴权**。

- Body：`hours` / `expiresAt` / `permanent` **三选一**（同时传 ≥2 或都不传 → 400）。
  - `hours`：整数 1~`MAX_HOURS`；在现有 `expiresAt` 上平移（永久链接禁止）。
  - `expiresAt`：epoch 毫秒整数、晚于当前、未来 365 天内；永久 → 限时也走此路。
  - `permanent`：转为永久（`expiresAt=0`）；已是永久 → 400。
- 成功 `200`：`{"ok":true,"data":<LinkView>}`。
- 错误：`400`（含 `请指定 hours、expiresAt 或 permanent 之一`、`请三选一…不能同时传`、`该链接已是永久有效` 等）。
- 副作用：改 `links.expires_at`；写审计（action=`extend`）。

```bash
curl -sS -b "$C" -X POST -H 'Content-Type: application/json' \
  -d '{"hours":12}' "$B/api/links/<id>/extend"
```

### DELETE /api/links/:id

删除链接（账本行 + 其 `traffic_samples`；`connections` 保留）。先尽力 `rmu`（失败不阻塞）。**需鉴权**。

- 成功 `200`：`{"ok":true}`（无 data）。
- 错误：`404 链接不存在`。
- 副作用：删 `links` 与其 `traffic_samples`；写审计（action=`link_delete`）；尽力调 `rmu`。

```bash
curl -sS -b "$C" -X DELETE "$B/api/links/<id>"
```

### GET /api/links/:id/traffic

流量曲线：按 30s 采样表聚合为时序。**需鉴权**。

- Query：
  | 名称 | 类型 | 默认 | 校验 |
  |---|---|---|---|
  | `bucket` | `hour` \| `day` | `hour` | 非法值回落 `hour` |
  | `from` | number(ms) | `to - 24h` | 夹到 `[1, to]` |
  | `to` | number(ms) | 当前时刻 | 夹到 `[1, MAX_SAFE_INTEGER]` |
- 成功 `200`：`{"ok":true,"data":[{"ts":<ms>,"up":<n>,"down":<n>}, …]}`（空桶补 0）。
- 副作用：无。

```bash
curl -sS -b "$C" "$B/api/links/<id>/traffic?bucket=hour"
```

### GET /api/links/:id/connections

连接记录分页 + 按 host 前缀搜索。**需鉴权**。

- Query：
  | 名称 | 类型 | 默认 | 校验 |
  |---|---|---|---|
  | `limit` | number | 50 | 夹到 `[1,200]` |
  | `offset` | number | 0 | 夹到 `[0, MAX_SAFE_INTEGER]` |
  | `q` | string | 无 | 去空白后按 200 字符截断，作 host 前缀 |
  | `from`/`to` | number(ms) | 无 | 缺省不过滤 |
- 成功 `200`：`{"ok":true,"data":{"rows":[…],"total":<n>,"limit":<n>,"offset":<n>}}`。
- 副作用：无。

```bash
curl -sS -b "$C" "$B/api/links/<id>/connections?limit=50&q="
```

### GET /api/audit

审计日志分页查询。**需鉴权**。

- Query：`limit`（默认 50，夹到 `[1,200]`）、`offset`（默认 0）、`from`/`to`（epoch 毫秒，缺省不过滤）。
- 成功 `200`：`{"ok":true,"data":{"rows":[{"id":<n>,"ts":<ms>,"actor":<string>,"action":<string>,"link_id":<string|null>,"detail":<object|null>}, …],"total":<n>,"limit":<n>,"offset":<n>}}`。
- `detail` 为原始 JSON（对象或 `null`），不二次转义。
- 副作用：无。

```bash
curl -sS -b "$C" "$B/api/audit?limit=20"
```

### GET /api/regions/probes

地区连通性快照 + 最近若干轮历史（内存态，不落库）。**需鉴权**。

- 成功 `200`：`{"ok":true,"data":{"updatedAt":<ms>,"probes":[{"key","ok","rttMs"|null,"error"|null,"ts","flag","name"}, …],"history":[…]}}`。
- 从未跑过时 `updatedAt=0`、`probes` 为空数组。
- 副作用：无。

```bash
curl -sS -b "$C" "$B/api/regions/probes"
```

### POST /api/clash/subscriptions

把所选节点打包成临时订阅地址（token 即凭据）。**需鉴权**。

- Body：`linkIds`（string[]，必填，1~50 条、每项非空字符串；重复自动去重）。
- 成功 `201`：`{"ok":true,"data":{"token":<string>,"url":<string>,"importUrl":<string>,"expiresAt":<ms>,"ttlSeconds":<n>,"count":<n>,"skipped":[<id>…],"nodes":[{"id","name"}…]}}`。
- 错误：`400 linkIds 须为数组` / `请选择 1~50 个节点` / `所选链接均不存在`。
- 副作用：写 `clash_subs` + 审计（action=`clash_sub_create`，detail 只记数量与 token 前缀，不落完整 token）。

```bash
curl -sS -b "$C" -X POST -H 'Content-Type: application/json' \
  -d '{"linkIds":["<id>"]}' "$B/api/clash/subscriptions"
```

### GET /api/clash/subscriptions/:token

抓取订阅正文（**免鉴权**，token 即凭据，Clash 抓取不带 Cookie）。**按链接当前状态重新生成 YAML。**

- Path：`token` 为订阅 token。
- Query：`mode`（`rule`（默认）\| `global`）。
- 成功 `200`：正文为 YAML，响应头：
  - `Content-Type: text/yaml; charset=utf-8`
  - `Content-Disposition: attachment; filename="v2link.yaml"`
  - `Cache-Control: no-store`
  - `subscription-userinfo: upload=<n>; download=<n>[; expire=<sec>]`（有信息时才带）
- 失败：`404` 纯文本中文（`订阅不存在` / `订阅已过期` / `所选节点均已失效`），`Content-Type: text/plain; charset=utf-8`。
- 副作用：无（只读）。

```bash
curl -sS "$B/api/clash/subscriptions/<token>?mode=rule"
```

---

## 5. 状态机

```
active ──(到期扫描)──▶ expired
active ──(吊销)──────▶ revoked
expired / revoked：不可再延长（extend 返回 400）
revoked：不可逆
expires_at = 0：永久哨兵，永不过期（过期扫描跳过）
```

- 到期扫描（默认 15s）把 `active` 且已到期（`expires_at > 0 && < now`）的行标 `expired` 并发 `rmu`。
- 账本为权威、xray 为镜像：一致性同步只读账本、不发账本写；xray 失败只记日志、下轮重试。

## 6. 有意差异

见仓库交付报告与 `docs/DB.md`。HTTP 契约层面已做到逐条对齐；已知近似点：

- 请求体非法 JSON 的 500 `error` 文案与 Node `body-parser` 的原文不同（消息形态一致：`{"ok":false,"error":…}`）。
- 查询串重复键只取首个值（近似 `qs` 的数组语义）；旧的业务字段都只读单值，行为一致。
- 静态响应不带 Express 的 `ETag` / `Last-Modified`（缓存头 `Cache-Control` 与旧实现一致）。
- 默认端口 `PORT` 由 7897 改为 19245（新旧并存，避免冲突）。
