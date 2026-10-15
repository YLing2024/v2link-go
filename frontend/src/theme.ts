import { useSyncExternalStore } from 'react'

// 主题三态（auto | light | dark）：
//   · 持久化在 localStorage 键 `theme_mode`，缺省 / 非法 / 不可用 → auto，不抛错。
//   · auto 实时跟随 window.matchMedia('(prefers-color-scheme: dark)') 的 change。
//   · 解析后的明暗写到 <html data-theme> 与 <meta name="color-scheme">；
//     首屏防闪白由 index.html 的内联脚本完成（在框架挂载前先跑一遍）。
//   · antd 的 algorithm（default/dark）由调用方按 resolved 选择，本模块不碰 antd。

export type ThemeMode = 'auto' | 'light' | 'dark'
export type ResolvedTheme = 'light' | 'dark'

const STORAGE_KEY = 'theme_mode'
const DARK_QUERY = '(prefers-color-scheme: dark)'

const listeners = new Set<() => void>()

function mediaQuery(): MediaQueryList | null {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return null
  try {
    return window.matchMedia(DARK_QUERY)
  } catch {
    return null
  }
}

/** 读取持久化的主题模式；读取失败 / 值非法 → auto */
export function getThemeMode(): ThemeMode {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (raw === 'light' || raw === 'dark' || raw === 'auto') return raw
  } catch {
    // localStorage 不可用（隐私模式等）→ 回退 auto
  }
  return 'auto'
}

/** 系统当前偏好（matchMedia 不可用 → 视为亮色） */
export function systemTheme(): ResolvedTheme {
  return mediaQuery()?.matches ? 'dark' : 'light'
}

/** 把三态解析成实际明暗 */
export function resolveTheme(mode: ThemeMode): ResolvedTheme {
  if (mode === 'light' || mode === 'dark') return mode
  return systemTheme()
}

let currentMode: ThemeMode = getThemeMode()

/** 当前解析后的明暗（供 antd algorithm 使用） */
export function getResolvedTheme(): ResolvedTheme {
  return resolveTheme(currentMode)
}

/** 把解析结果写到 <html data-theme> 与 color-scheme（meta + style） */
function applyTheme(resolved: ResolvedTheme): void {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  root.setAttribute('data-theme', resolved)
  root.style.colorScheme = resolved
  const meta = document.querySelector('meta[name="color-scheme"]')
  if (meta) meta.setAttribute('content', resolved)
}

/** 切换 / 设置主题模式：持久化 + 立即生效 + 通知订阅者 */
export function setThemeMode(mode: ThemeMode): void {
  currentMode = mode
  try {
    window.localStorage.setItem(STORAGE_KEY, mode)
  } catch {
    // 写失败不致命：本次会话仍生效
  }
  applyTheme(resolveTheme(mode))
  listeners.forEach((listener) => listener())
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

// 监听系统偏好变化：auto 时实时跟随；非 auto 时也会重算（结果不变，幂等）。
mediaQuery()?.addEventListener('change', () => {
  applyTheme(resolveTheme(currentMode))
  listeners.forEach((listener) => listener())
})

// 模块加载即应用一次（内联脚本已先跑；此处保证 React 侧状态与 DOM 一致）
applyTheme(getResolvedTheme())

/** React 侧读取当前主题：{ mode, resolved, setMode }；系统偏好变化时也会触发重渲染 */
export function useThemeMode(): {
  mode: ThemeMode
  resolved: ResolvedTheme
  setMode: (mode: ThemeMode) => void
} {
  const mode = useSyncExternalStore(subscribe, getThemeMode, () => 'auto' as ThemeMode)
  const resolved = useSyncExternalStore(subscribe, getResolvedTheme, () => 'light' as ResolvedTheme)
  return { mode, resolved, setMode: setThemeMode }
}
