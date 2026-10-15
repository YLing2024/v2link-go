import type { RegionProbeResult } from '../types'

// 地区连通性（地区连通性监控）展示辅助。
// 颜色阈值与需求一致：绿=正常 <300ms；黄=慢 300~800ms；红=失败/无数据。

export const RTT_GREEN = 300
export const RTT_YELLOW = 800

export type RegionTone = 'green' | 'yellow' | 'red'

export function toneOf(r: RegionProbeResult | undefined): RegionTone {
  if (!r) return 'red'
  if (!r.ok) return 'red'
  if (r.rttMs === null) return 'red'
  if (r.rttMs < RTT_GREEN) return 'green'
  if (r.rttMs <= RTT_YELLOW) return 'yellow'
  return 'red'
}

export function toneLabel(t: RegionTone): string {
  if (t === 'green') return '正常'
  if (t === 'yellow') return '偏慢'
  return '不可达'
}

export function probeLabel(r: RegionProbeResult | undefined): string {
  if (!r) return '—'
  if (r.ok && r.rttMs !== null) return `${r.rttMs}ms`
  return r.error ?? '失败'
}
