// 与后端 LinkView 对应（见 server/src/types.ts）
export type LinkStatus = 'active' | 'expired' | 'revoked'

export interface Link {
  id: string
  uuid: string
  note: string
  /** 客户端节点名（vless #fragment）；空串 = 未设置，回退 note → id */
  alias: string
  upBytes: number
  downBytes: number
  createdAt: number
  /** 永久链接为哨兵 0；展示/判定请用 permanent */
  expiresAt: number
  /** 永久有效（永不过期）；由后端按 expires_at 推导 */
  permanent: boolean
  revokedAt: number | null
  status: LinkStatus
}

export interface ApiResponse<T> {
  ok: boolean
  data?: T
  error?: string
}

export interface CreateLinkBody {
  note?: string
  /** 客户端节点名（写入 vless #fragment） */
  alias?: string
  /** 绝对过期时刻（epoch ms，精确到分钟）；与 hours / permanent 三选一 */
  expiresAt?: number
  hours?: number
  /** true = 永久有效（与 hours / expiresAt 互斥） */
  permanent?: boolean
}

// ---- 监控/追溯/审计（监控/追溯/审计）----

// 流量曲线桶点（A）
export interface TrafficPoint {
  ts: number
  up: number
  down: number
}

// 连接记录行（B）
export interface ConnectionRecord {
  id: number
  link_id: string | null
  email: string
  ts: number
  host: string | null
  port: number | null
  up_bytes: number | null
  down_bytes: number | null
  duration_ms: number | null
}

// 审计日志行（C）
export interface AuditRecord {
  id: number
  ts: number
  actor: string
  action: 'create' | 'revoke' | 'extend' | 'link_delete' | 'clash_sub_create'
  link_id: string | null
  detail: Record<string, unknown> | null
}

// ---- Clash 订阅 ----

// POST /api/clash/subscriptions 出参
export interface ClashSubResult {
  token: string
  /** 订阅地址（二维码 / 复制用） */
  url: string
  /** clash:// 一键导入链接 */
  importUrl: string
  expiresAt: number
  ttlSeconds: number
  count: number
  /** 入参里被忽略（不存在）的链接 id */
  skipped: string[]
  nodes: { id: string; name: string }[]
}

// 分页信封（/connections 与 /audit 共用）
export interface Paged<T> {
  rows: T[]
  total: number
  limit: number
  offset: number
}

// ---- 地区连通性监控（地区连通性监控）----

export interface RegionProbeResult {
  key: string
  ok: boolean
  rttMs: number | null
  error: string | null
  ts: number
}

export interface RegionProbeSnapshot {
  updatedAt: number
  probes: (RegionProbeResult & { flag: string; name: string })[]
  history: { ts: number; results: RegionProbeResult[] }[]
}
