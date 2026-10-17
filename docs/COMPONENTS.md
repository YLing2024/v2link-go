# 前端组件登记（COMPONENTS.md）

本文件登记 `frontend/src/components/` 下的可复用组件。

**说明**：`frontend/` 是从旧仓库（`/root/proj/v2link/frontend`）**原样搬入**的（需求书 §5），
本轮未改动任何组件源码，因此组件文件头的注释沿用旧仓库现状（旧仓库未强制 TSDoc）。
本表按工程基线 §11 的要求补一份登记；**后续新增或改动可复用组件时，必须同笔更新本表并为文件头补 TSDoc**。

- 设计语言：antd 5 默认（主色 `#1677ff`、圆角 6px、系统字体栈），不映射自定义调色板。
- 样式：唯一 CSS 为 `src/styles/style.css`（仅布局 glue + antd 无对应件的 carve-out）。
- 窄屏 / 触摸适配：响应式规则集中在 `src/styles/style.css` 末尾的
  `@media (max-width: 991px), (pointer: coarse)` 块内（≥992px 且精细指针不进入，桌面观感不变）：
  可见文本字号抬到 ≥12px、主要操作按钮 ≥44px、关闭/图标 ≥32×32、表单控件 ≥44px、
  相邻目标间距 ≥8px（`Space` 间距是内联样式，用 `.touch-gap` + `!important` 兜底）。
  机检见 `src/components/responsive.test.tsx`。
- 不引入 Storybook。
- 组件类型定义来自 `frontend/src/types.ts`（如 `Link`、`TrafficPoint`、`AuditRecord`）。

| 组件 | 用途 | 关键 props（默认值） | 最小示例 | 注意事项 |
|---|---|---|---|---|
| `Dashboard`（默认导出） | 主面板：链接表格、操作入口、地区状态条、审计入口。 | 无 | `import Dashboard from './components/Dashboard'` | 承载页面级状态；非可复用展示件。 |
| `Login`（默认导出） | 内置账号登录表单（builtin 模式）。 | `onSuccess: (name) => void` | `<Login onSuccess={setUser} />` | `sso` 模式下由外部决定不渲染；提交走 `/api/auth/login`。 |
| `CreateModal` | 新建链接弹窗（时长档位 / 绝对时刻 / 永久）。 | `onClose: () => void`、`onCreated: () => void` | `<CreateModal onClose={fn} onCreated={reload} />` | 导出 `QUICK_EXPIRY` 档位常量；创建优先级与后端一致（permanent > expiresAt > hours）。 |
| `CopyModal` | 复制 vless 链接 + 二维码展示。 | `link: Link`、`onClose: () => void` | `<CopyModal link={l} onClose={fn} />` | 节点名走 `alias → note → id` 回退；剪贴板不可用时回退手动选择。 |
| `ActModal` | 吊销 / 延长操作弹窗。 | `link: Link`、`onClose: () => void`、`onDone: (updated: Link) => void` | `<ActModal link={l} onClose={fn} onDone={fn2} />` | 三选一语义（hours / expiresAt / permanent）与后端校验一致。 |
| `LinkDetailModal` | 链接详情：流量曲线 + 连接记录。 | `link: Link`、`onClose: () => void` | `<LinkDetailModal link={l} onClose={fn} />` | 流量走 `/traffic`，连接分页走 `/connections`；连接表 `scroll.x`，窄屏可横滑。 |
| `AuditModal` | 审计日志查看（分页）。 | `onClose: () => void` | `<AuditModal onClose={fn} />` | 行动 `action` 到状态的色值映射在本文件内维护；表格 `scroll.x`，窄屏可横滑。 |
| `ClashSubModal` | 生成 / 抓取 Clash 临时订阅。 | `linkIds: string[]`、`onClose: () => void` | `<ClashSubModal linkIds={ids} onClose={fn} />` | 订阅 token 即凭据；创建需鉴权，抓取免鉴权。 |
| `TrafficChart`（默认导出） | ECharts 流量图（`React.lazy` 独立 chunk）。 | `points: TrafficPoint[]` | `<TrafficChart points={pts} />` | 颜色取 antd CSS 变量，主题切换时重设 option。 |
| `TrafficChartImpl`（默认导出） | `TrafficChart` 的实际实现（被 lazy 加载）。 | `points: TrafficPoint[]` | 见 `TrafficChart.tsx` | 不直接引用；算法在 `lib/trafficChart.ts`（纯函数，有单测）；轴标签字号在窄屏/触摸下 10→12px。 |
| `RegionProbeBar` | 底部地区连通性状态条。 | 无 | `<RegionProbeBar />` | 轮询 `/api/regions/probes`；失败不致命。 |
| `ThemeSwitch` | 顶栏主题三态切换（自动 / 亮色 / 暗色）。 | `block = false` | `<ThemeSwitch block />` | 窄屏收进抽屉；三态存 `theme`。 |
| `Modal`（默认导出） | 通用弹窗（antd Modal 薄封装）。 | `title: string`、`onClose: () => void`、`children: ReactNode`、`width = 420` | `<Modal title="标题" onClose={fn}>…</Modal>` | Esc / 遮罩关闭沿用 antd 默认；`footer=null`，按钮由调用方排布。 |
