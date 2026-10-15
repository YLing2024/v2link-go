# 验收记录（Go 重写）

本文件记录对本仓库的验收方法与**真实结果**。验收脚本在本地一次性编写并运行，位于仓库外
（`.acceptance/`，已 gitignore）——其中包含现网库副本与容器口令等，**不纳入版本库**；本文件
只保留去敏后的命令形态与结论。所有地址用 `127.0.0.1`，域名用 `example.com`。

对照基准：旧实现 `/root/proj/v2link`（Node 24 + Express 4 + SQLite，只读，全程未修改）。
新实现运行端口一律 `127.0.0.1` 上的验收临时端口，从不触碰生产端口。

## 1. 新旧并排对照

- 方法：起一对实例（旧 :19246 / 新 :19248，`AUTH_MODE=sso`，均用现网库副本 + 假 xray），
  同一请求矩阵逐条比状态码、响应体形状、关键响应头；另跑一轮写操作（建/延长/吊销/删除/审计/Clash）。
- 结果：24/24 请求有响应；23 条一致，1 条为**有意差异**（非法 JSON 请求体：旧 Express body-parser
  的 500 错误文案与新实现不同，见 `docs/API.md`「有意差异」）。写操作全部一致；Clash YAML 逐字节一致；
  `subscription-userinfo` 一致；静态资源缓存头一致。

## 2. 假 xray 旁路

- 方法：`XRAY_BIN` 指向 stub 脚本，记录 `api adu/rmu` 的真实调用参数并落地状态文件。
- 结果全部 PASS：创建→`adu`（片段 tag `vless-in`、email、protocol 正确）；稳态幂等零动作零日志；
  吊销→`rmu`；过期→`rmu`；xray 重启（清空 stub 状态）后下轮全量补回；xray 失败→502 且
  **账本（links/audit 条数）不变**、下轮重试。全程未对生产 xray 做任何写操作。

## 3. 旧库直读

- 方法：现网 `v2link.db` 经 SQLite backup API 快照后由新实现打开，比对列表/审计/采样/连接条数。
- 结果：条数一致（links 6 / audit 49 / samples 472 / connections 1842/239）；仅新增
  `schema_version` 表；源库 sha256 前后不变（`cb70c9c9…7313a`）。

## 4. 双库对等

- 方法：同一套请求分别打 `DB_DRIVER=sqlite`（:19252）与 `DB_DRIVER=mysql`（:19253，临时 MariaDB
  容器仅监听 `127.0.0.1`），响应归一后 diff；再跑 `migrate-data --dry-run` / `--verify`
  （sqlite 快照 → MySQL）。
- 结果：两份响应签名**逐字节一致**（0 不一致）；两库落库行数一致；`--dry-run` 条数正确；
  `--verify` 全部 OK（links 6 / samples 472 / connections 1842 / audit 49）。容器验完即停并删除。

## 5. 不动现有库

- 方法：源码 grep 确认无 `DROP`/`TRUNCATE`/`RENAME TABLE`（仅业务 `DELETE FROM`）；构造含
  `speed_mbps` 历史列、`users.created_at` 为 TEXT 的老库，交新实现打开。
- 结果：数据保留、`speed_mbps` 列保留、`alias` 列被增量补上、接口返回正确；迁移只做
  `CREATE TABLE IF NOT EXISTS` / `ADD COLUMN` / `CREATE INDEX`。

## 6. access log 轮转

- 方法：合成访问日志验证增量读取、`copytruncate`、rename 轮转、半行补齐。
- 结果：增量读取不重复；copytruncate 后不丢行；rename 轮转后不丢不重；稳态零重复；
  半行先缓存、补全后再计。全部符合预期。

## 7. Clash 订阅

- 方法：旧 :19261 / 新 :19262（`CLASH_SUB_TTL_S=3`）并排，覆盖免鉴权抓取、响应头契约、
  rule/global/非法 mode、过期 404、节点全失效 404、未知 token 404。
- 结果：0 不一致。`Content-Type: text/yaml; charset=utf-8`、`Cache-Control: no-store`、
  `Content-Disposition`、`subscription-userinfo` 新旧一致；YAML 归一后逐字节一致（rule 1337 字节、
  global 379 字节）；非法 mode 回落 rule；过期 404 文案「订阅已过期」、全失效「所选节点均已失效」、
  未知 token「订阅不存在」均与旧实现一致；免鉴权可直接抓取。纯函数层面另有
  `internal/clash/clash_test.go` 锁定规则顺序、重名去重与 YAML 输出。

## 8. 认证

- 方法：旧 :19263 / 新 :19264（`AUTH_MODE=builtin`）并排。
- 结果：0 不一致。`auth-mode`、未登录 401、错误口令/缺字段 401、登录 200 响应体、`Set-Cookie`
  属性与顺序（`Max-Age; Path; Expires; HttpOnly; SameSite=Lax`）一致；滑动续期生效；
  登出 200 且清 cookie、幂等；登出后 `me` 401；10 次失败后第 11 次 429 且 `retryAfter` 一致。
  `sso` 模式下 login/logout/me 一律 404（见第 1 项矩阵）。

## 9. 前端

- 方法：用无头 Chromium（Playwright 驱动）真实操作新控制面自带的 SPA：登录 → 生成链接 →
  复制/二维码弹窗（`vless://` + dataURL 二维码）→ 勾选生成 Clash 订阅（订阅链接、`clash://`
  导入链接、订阅二维码）。
- 结果：0 不一致；无未捕获异常；控制台仅有首屏未登录探测 `/api/auth/me` 的 **预期 401**，
  无其它 error；无失败网络请求。

## 10. 资源

| 指标 | 新实现（Go） | 旧实现（Node 24） |
|---|---|---|
| 单二进制体积 | 12.9 MiB | —（Node 运行时另计） |
| 空载 RSS | 22 MiB | 92 MiB |
| 冷启动→`/api/healthz` 200 | 102 ms | 484 ms |

## 复跑

`.acceptance/` 下的脚本（`servers.sh` / `matrix.mjs` / `write-parity.mjs` / `dual-db.sh` /
`fake-xray.sh` / `accesslog-rotation.sh` / `clash-live.sh` / `auth-parity.sh` / `fe-e2e.mjs` /
`resources.sh`）为一次性验收脚手架，不入库。持久化的回归由仓库内单测承担：
`internal/vless/vless_test.go`、`internal/clash/clash_test.go`、`internal/store/store_test.go`、
`internal/scrypt/scrypt_test.go`，以及 `make check`（路由↔文档机检 + `go vet` + 单测）。
