# 验收记录（Go 重写）

本文件记录对本仓库的验收方法与**真实结果**。验收脚本在本地一次性编写并运行，位于仓库外
（`.acceptance/`，已 gitignore）——其中包含现网库副本与容器口令等，**不纳入版本库**；本文件
只保留去敏后的命令形态与结论。所有地址用 `127.0.0.1`，域名用 `example.com`。

存储口径（2026-10-16）：新实现**只支持 MySQL**（`github.com/go-sql-driver/mysql`），
建表/增量迁移全在 `internal/store`；旧 SQLite 数据由独立工具 `tools/migrate-sqlite-to-mysql/`
一次性搬运（独立 module，不被服务 import）。

对照基准：旧实现 `/root/proj/v2link`（Node 24 + Express 4 + SQLite，只读，全程未修改）。
新实现运行端口一律 `127.0.0.1` 上的验收临时端口，从不触碰生产端口；MySQL 用一次性容器，
只监听 `127.0.0.1` 高位端口，验完即停并删除。

## 1. 新旧并排对照

- 方法：起一对实例（旧 :19246 / 新 :19248，`AUTH_MODE=sso` + 假 xray）。旧实现仍用 SQLite
  快照副本；新实现用同一快照经 `tools/migrate-sqlite-to-mysql` 装载进的临时 MySQL 库。
  同一请求矩阵逐条比状态码、响应体形状、关键响应头；另跑一轮写操作（建/延长/吊销/删除/审计/Clash）。
- 结果：24/24 请求有响应；23 条一致，1 条为**有意差异**（非法 JSON 请求体：旧 Express
  body-parser 的 500 错误文案与新实现不同，见 `docs/API.md`「有意差异」）。写操作全部一致；
  Clash YAML 逐字节一致；`subscription-userinfo` 一致；静态资源缓存头一致。

## 2. 假 xray 旁路

- 方法：`XRAY_BIN` 指向 stub 脚本，记录 `api adu/rmu` 的真实调用参数并落地状态文件。
- 结果全部 PASS：创建→`adu`（片段 tag `vless-in`、email、protocol 正确）；稳态幂等零动作零日志；
  吊销→`rmu`；过期→`rmu`；xray 重启（清空 stub 状态）后下轮全量补回；xray 失败→502 且
  **账本（links/audit 条数）不变**、下轮重试。全程未对生产 xray 做任何写操作。

## 3. 旧库搬运（SQLite → MySQL）

- 方法：现网 `v2link.db` 经 SQLite backup API 快照（**只读**打开：`mode=ro&query_only=1`），
  由独立工具 `tools/migrate-sqlite-to-mysql/` 逐表 源计数→拷贝→目标计数→抽样比对；
  默认即搬运，`--dry-run` 只出计划，`--verify` 只做校验。
- 结果：源 2369 行 → 目标 2369 行（links 6 / traffic_samples 472 / connections 1842 /
  audit_log 49，clash_subs、users、sessions 0），每表抽样 5 行 **OK**；源库
  sha256 前后不变（`cb70c9c9…7313a`）。

## 4. 仅 MySQL 存储

- 方法：空 MySQL 库直接启动新实现（`store.Open` → 幂等 `Migrate`），断言自建全部表、索引、列；
  再以 `TEST_DB_HOST/PORT/NAME/USER/PASSWORD` 指向临时容器跑 `make check`。
- 结果：空库自建 `links / traffic_samples / connections / audit_log / clash_subs / users /
  sessions / schema_version` 八表与全部索引（`uq_links_uuid`/`uq_links_email`/`uq_users_username`/
  `idx_*`），重复迁移幂等、无多余表；`schema_version` 落版本记录。`make check` 全绿
  （路由↔文档机检 + `go vet` + 单测，`internal/store` 用例真跑在 MySQL 上）。DDL 与
  `docs/mysql-schema-cloud.md` 的云库 `v2link` 逐列核对一致。

## 5. 不动现有库（只做增量）

- 方法：造一个 `links` 缺 `alias`、多历史列 `speed_mbps` 的 MySQL 老库并写入一行，交新实现
  启动；另 grep 源码确认无 `DROP`/`TRUNCATE`/`RENAME TABLE`。
- 结果：`alias` 经 `ALTER TABLE ADD COLUMN` 增量补上；`speed_mbps` 与历史行数据都保留；
  接口正确返回该行。源码中只有注释提到 `DROP`/`TRUNCATE`（说明「绝不」），无执行语句；
  迁移只做 `CREATE TABLE IF NOT EXISTS` / `ADD COLUMN` / `CREATE INDEX`。

## 6. access log 轮转

- 方法：MySQL 空库启动，合成访问日志验证增量读取、`copytruncate`、rename 轮转、半行补齐。
- 结果：2 → 3 → 4（不丢不重）；稳态重读仍为 4（不重复计数）；尾行半截先缓存、补全后计为 5。
  全部符合预期。

## 7. Clash 订阅

- 方法：旧 :19261（SQLite 快照）/ 新 :19262（MySQL 装载同一快照，`CLASH_SUB_TTL_S=3`）并排，
  覆盖免鉴权抓取、响应头契约、rule/global/非法 mode、过期 404、节点全失效 404、未知 token 404。
- 结果：0 不一致。`Content-Type: text/yaml; charset=utf-8`、`Cache-Control: no-store`、
  `Content-Disposition`、`subscription-userinfo` 新旧一致；YAML 归一后逐字节一致（rule 1337 字节、
  global 379 字节）；非法 mode 回落 rule；过期 404 文案「订阅已过期」、全失效「所选节点均已失效」、
  未知 token「订阅不存在」均与旧实现一致；免鉴权可直接抓取。纯函数层面另有
  `internal/clash/clash_test.go` 锁定规则顺序、重名去重与 YAML 输出。

## 8. 认证

- 方法：旧 :19263（SQLite 新库）/ 新 :19264（MySQL 空库，`AUTH_MODE=builtin`）并排。
- 结果：0 不一致。`auth-mode`、未登录 401、错误口令/缺字段 401、登录 200 响应体、`Set-Cookie`
  属性与顺序（`Max-Age; Path; Expires; HttpOnly; SameSite=Lax`）一致；滑动续期生效（由 MySQL
  `sessions.expires_at` 前后比对确认：`1792128748501 → 1792128749920`）；登出 200 且清 cookie、
  幂等；登出后 `me` 401；10 次失败后第 11 次 429 且 `retryAfter` 一致。
  `sso` 模式下 login/logout/me 一律 404（见第 1 项矩阵）。

## 9. 前端

- 方法：新实例（:19270，MySQL 空库，`AUTH_MODE=builtin`）用无头 Chromium（Playwright 驱动）
  真实操作自带 SPA：登录 → 生成链接 → 复制/二维码弹窗（`vless://` + dataURL 二维码）→
  勾选生成 Clash 订阅（订阅链接、`clash://` 导入链接、订阅二维码）。
- 结果：0 不一致；无未捕获异常；控制台仅有首屏未登录探测 `/api/auth/me` 的 **预期 401**，
  无其它 error；无失败网络请求。

## 10. 资源

| 指标 | 新实现（Go + MySQL） | 旧实现（Node 24 + SQLite） |
|---|---|---|
| 单二进制体积 | 9.1 MiB | —（Node 运行时另计） |
| 空载 RSS | 17 MiB | 94 MiB |
| 冷启动→`/api/healthz` 200 | 239 ms | 566 ms |

## 复跑

`.acceptance/` 下的脚本（`servers.sh` / `matrix.mjs` / `write-parity.mjs` / `mysql.sh` /
`legacy-mysql.sh` / `fake-xray.sh` / `accesslog-rotation.sh` / `clash-live.sh` /
`auth-parity.sh` / `fe-e2e.mjs` / `resources.sh`）为一次性验收脚手架，不入库。新实现的每个
实例都先由 `mysql.sh` 在临时容器上新建库（空库自建表，或经迁移工具装载快照）。持久化的回归由
仓库内单测承担：`internal/vless/vless_test.go`、`internal/clash/clash_test.go`、
`internal/store/store_test.go`、`internal/scrypt/scrypt_test.go`，以及 `make check`
（路由↔文档机检 + `go vet` + 单测，`store` 集成用例读 `TEST_DB_*` 在 MySQL 上真跑）。
