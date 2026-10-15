// 本地时间 ⇄ epoch ms 互转（datetime-local 输入用，分钟精度）。
// 独立成模块：CreateModal（生成时设过期时刻）与 ActModal（编辑过期时刻）共用。

/** datetime-local 输入 → epoch ms（输入为空/非法返回 null） */
export function toEpochMs(dtLocal: string): number | null {
  if (!dtLocal) return null
  const d = new Date(dtLocal)
  return Number.isNaN(d.getTime()) ? null : d.getTime()
}

/** epoch ms → datetime-local 可见字符串（本地时区，无秒；浏览器控件按分钟选） */
export function toDateTimeLocal(ms: number): string {
  const d = new Date(ms)
  const p = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

/** 生成弹窗默认过期时刻：当前 + 默认档（24h） */
export function defaultExpiry(now: number = Date.now()): number {
  return now + 24 * 3600 * 1000
}
