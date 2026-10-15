import { lazy, Suspense } from 'react'
import type { TrafficPoint } from '../types'

// 流量折线入口：ECharts 实现走动态 import，独立成 chunk（不进主入口）。
// 组件对外 props 不变（{ points }）；加载期间给空态占位。
const TrafficChartImpl = lazy(() => import('./TrafficChartImpl'))

export default function TrafficChart({ points }: { points: TrafficPoint[] }) {
  return (
    <Suspense fallback={<div className="chart-empty">加载中…</div>}>
      <TrafficChartImpl points={points} />
    </Suspense>
  )
}
