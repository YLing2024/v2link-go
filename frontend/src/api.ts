// 统一 API client。认证模式由后端 /api/auth-mode 决定（启动探测一次并缓存）：
//   builtin：自带账号会话（HttpOnly cookie，同源自动携带）；401 切回本地登录页。
//   sso：关掉自带口令，身份由前置认证层决定；401 整页跳 /_auth/login?next=...
// 探测失败一律按 sso 处理（绝不回退 builtin）。

import type {
  AuditRecord,
  ClashSubResult,
  ConnectionRecord,
  Link,
  Paged,
  RegionProbeSnapshot,
  TrafficPoint,
} from './types'

export class ApiError extends Error {
  status: number
  /** 429 时服务端给出的重试秒数 */
  retryAfter?: number
  constructor(status: number, message: string, retryAfter?: number) {
    super(message)
    this.status = status
    this.retryAfter = retryAfter
  }
}

export interface ApiOptions {
  method?: string
  body?: unknown
}

export type AuthMode = 'builtin' | 'sso'

export interface AuthUser {
  name: string
}

// ---- 认证模式探测（缓存；失败按 sso）----
let authModePromise: Promise<AuthMode> | null = null

export function getAuthMode(): Promise<AuthMode> {
  if (!authModePromise) {
    authModePromise = fetch('/api/auth-mode', { credentials: 'same-origin' })
      .then((res) => (res.ok ? res.json() : null))
      .then((data: { authMode?: string } | null) =>
        data?.authMode === 'builtin' ? ('builtin' as const) : ('sso' as const),
      )
      .catch(() => 'sso' as const)
  }
  return authModePromise
}

// builtin 下 401 的本地回调（App 挂载：切回登录页）
let onUnauthorized: (() => void) | null = null
export function setUnauthorizedHandler(fn: (() => void) | null): void {
  onUnauthorized = fn
}

let redirecting = false

// sso：401 → 整页跳前置认证登录页，next 带回当前地址（已在 /_auth/ 页面上不再重复跳，防 next 嵌套）
export function redirectToLogin(): void {
  if (redirecting) return
  if (window.location.pathname.startsWith('/_auth/')) return
  redirecting = true
  const next = encodeURIComponent(window.location.pathname + window.location.search)
  window.location.href = `/_auth/login?next=${next}`
}

/** builtin：登录本地账号 */
export async function login(username: string, password: string): Promise<AuthUser> {
  const res = await fetch('/api/auth/login', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  const data = (await res.json().catch(() => ({}))) as {
    error?: string
    retryAfter?: number
    user?: { name?: string }
  }
  if (res.status === 429) {
    throw new ApiError(429, data.error || '尝试过于频繁，请稍后再试', data.retryAfter)
  }
  if (!res.ok) throw new ApiError(res.status, data.error || `HTTP ${res.status}`)
  return { name: data.user?.name ?? username }
}

/** builtin：当前登录用户；未登录返回 null */
export async function me(): Promise<AuthUser | null> {
  const res = await fetch('/api/auth/me', { credentials: 'same-origin' })
  if (!res.ok) return null
  const data = (await res.json().catch(() => null)) as {
    name?: string
    user?: { name?: string }
  } | null
  const name = data?.name ?? data?.user?.name
  return name ? { name } : null
}

/** 退出：按模式分发（sso 交给前置认证层；builtin 清本地会话并切回登录页） */
export async function logout(): Promise<void> {
  const mode = await getAuthMode()
  if (mode === 'sso') {
    window.location.href = '/_auth/logout'
    return
  }
  try {
    await fetch('/api/auth/logout', { method: 'POST', credentials: 'same-origin' })
  } catch {
    // 网络失败也切回登录页；cookie 仍由服务端会话决定
  }
  onUnauthorized?.()
}

async function request<T>(path: string, options: ApiOptions = {}): Promise<T> {
  const headers: Record<string, string> = {}
  const opts: RequestInit = {
    method: options.method ?? 'GET',
    headers,
    credentials: 'same-origin',
  }
  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(options.body)
  }

  const res = await fetch(path, opts)
  const data = (await res.json().catch(() => ({}))) as {
    ok?: boolean
    data?: T
    error?: string
  }

  if (res.status === 401) {
    const mode = await getAuthMode()
    if (mode === 'sso') redirectToLogin()
    else onUnauthorized?.()
    throw new ApiError(401, data.error || '未登录或登录已过期')
  }
  if (!res.ok) {
    throw new ApiError(res.status, data.error || `HTTP ${res.status}`)
  }
  if (data && data.ok === false) {
    throw new ApiError(res.status, data.error || '请求失败')
  }
  return data.data as T
}

export function listLinks(): Promise<Link[]> {
  return request<Link[]>('/api/links')
}

export function createLink(body: {
  note?: string
  alias?: string
  expiresAt?: number
  hours?: number
  permanent?: boolean
}): Promise<Link> {
  return request<Link>('/api/links', { method: 'POST', body })
}

export function revokeLink(id: string): Promise<Link> {
  return request<Link>(`/api/links/${encodeURIComponent(id)}/revoke`, { method: 'POST' })
}

/** 删除链接（账本行 + 采样；connections 保留）。成功回 { ok: true }（无 data）。 */
export function deleteLink(id: string): Promise<void> {
  return request<void>(`/api/links/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function extendLink(
  id: string,
  body: { expiresAt?: number; hours?: number; permanent?: boolean },
): Promise<Link> {
  return request<Link>(`/api/links/${encodeURIComponent(id)}/extend`, {
    method: 'POST',
    body,
  })
}

export function regionProbes(): Promise<RegionProbeSnapshot> {
  return request<RegionProbeSnapshot>('/api/regions/probes')
}

export function linkTraffic(id: string, bucket: 'hour' | 'day', from?: number, to?: number): Promise<TrafficPoint[]> {
  const qs = new URLSearchParams({ bucket })
  if (from !== undefined) qs.set('from', String(from))
  if (to !== undefined) qs.set('to', String(to))
  return request<TrafficPoint[]>(`/api/links/${encodeURIComponent(id)}/traffic?${qs}`)
}

export function linkConnections(
  id: string,
  opts: { q?: string; from?: number; to?: number; limit?: number; offset?: number } = {},
): Promise<Paged<ConnectionRecord>> {
  const qs = new URLSearchParams()
  if (opts.q) qs.set('q', opts.q)
  if (opts.from !== undefined) qs.set('from', String(opts.from))
  if (opts.to !== undefined) qs.set('to', String(opts.to))
  if (opts.limit !== undefined) qs.set('limit', String(opts.limit))
  if (opts.offset !== undefined) qs.set('offset', String(opts.offset))
  return request<Paged<ConnectionRecord>>(`/api/links/${encodeURIComponent(id)}/connections?${qs}`)
}

export function auditLog(opts: { limit?: number; offset?: number } = {}): Promise<Paged<AuditRecord>> {
  const qs = new URLSearchParams()
  if (opts.limit !== undefined) qs.set('limit', String(opts.limit))
  if (opts.offset !== undefined) qs.set('offset', String(opts.offset))
  return request<Paged<AuditRecord>>(`/api/audit?${qs}`)
}

/** 生成 Clash 订阅（多选节点 → 10 分钟有效订阅地址）。linkIds 顺序 = 勾选顺序 */
export function createClashSubscription(linkIds: string[]): Promise<ClashSubResult> {
  return request<ClashSubResult>('/api/clash/subscriptions', {
    method: 'POST',
    body: { linkIds },
  })
}
