import type { TrafficPoint } from '../types'

// 轻量 SVG 折线（不引重图表库，监控）。Swiss 极简：单轴 + 双色折线。
// 纯函数算 path，组件只做 viewBox 渲染，方便单测。空数据由父级给空态。

export function maxVal(points: TrafficPoint[]): number {
  let m = 0
  for (const p of points) {
    if (p.up > m) m = p.up
    if (p.down > m) m = p.down
  }
  return m
}

export interface ChartGeom {
  max: number
  /** 下行折线 path（无数据/单点时为 ''） */
  down: string
  /** 上行折线 path（可空） */
  up: string
  /** 下行面积填充 path（收口底边；单点为空） */
  area: string
}

export const W = 640
export const H = 110
export const PAD = 6

// 颜色不再写死在这里：折线/面积改由 CSS 类着色（chart-line-down / chart-line-up /
// chart-area），这样深浅色主题都能跟随（见 styles/style.css）。

export function chartGeom(points: TrafficPoint[]): ChartGeom {
  const max = maxVal(points)
  const n = points.length
  if (n === 0 || max <= 0) return { max, down: '', up: '', area: '' }
  const innerW = W - PAD * 2
  const innerH = H - PAD * 2
  const x = (i: number) => (n <= 1 ? W / 2 : PAD + (i / (n - 1)) * innerW)
  const y = (v: number) => PAD + innerH - (v / max) * innerH
  const line = (key: 'up' | 'down') =>
    points
      .map((p, i) => `${i === 0 ? 'M' : 'L'} ${x(i)} ${y(p[key])}`)
      .join(' ')
  const down = line('down')
  const upTotal = points.reduce((s, p) => s + p.up, 0)
  const up = upTotal > 0 ? line('up') : ''
  const area =
    n > 1 ? `${down} L ${x(n - 1)} ${H - PAD} L ${x(0)} ${H - PAD} Z` : ''
  return { max, down, up, area }
}
