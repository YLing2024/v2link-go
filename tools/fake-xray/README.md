# tools/fake-xray

验收专用的假 xray，把控制面发起的 `xray api adu/rmu/statsquery` 调用记录下来，
并按真实 xray 的 stdout 契约回话。**只用于本地验收，绝不参与生产部署，也绝不接触真实 xray。**

## 契约

- 调用日志：每次调用把完整 argv 追加一行到 `FAKE_XRAY_LOG`。
- 用户状态：`FAKE_XRAY_STATE` 下每个用户一个 `.cont` 文件（`<uuid> <up> <down>`）。
- `adu`：已存在 → stdout `failed to add user …: user already exists`；新增 → `Added 1 user(s) to inbound`。
- `rmu`：存在 → `Removed 1 user(s)`；不存在 → `Removed 0 user(s)`。
- `statsquery`：把状态里的 up/down 输出为 xray stats JSON；带 `-reset` 时输出后清零。
- `FAKE_XRAY_FAIL=1`：`adu`/`rmu` 一律非零退出，用于验证「xray 失败时账本不变、下轮重试」。

## 用法

```bash
chmod +x tools/fake-xray/xray
export XRAY_BIN="$PWD/tools/fake-xray/xray"
export FAKE_XRAY_LOG="$PWD/.acceptance/logs/xray.log"
export FAKE_XRAY_STATE="$PWD/.acceptance/xray-state"
```
