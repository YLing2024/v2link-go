import { useEffect, useMemo, useRef } from 'react'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import type { LineSeriesOption } from 'echarts/charts'
import {
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
} from 'echarts/components'
import type {
  GridComponentOption,
  TooltipComponentOption,
  LegendComponentOption,
  DataZoomComponentOption,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { formatBytes } from '../lib/format'
import { maxVal } from '../lib/trafficChart'
import { useThemeMode } from '../theme'
import type { TrafficPoint } from '../types'

// 流量折线（ECharts）。本文件由 TrafficChart 动态 import，独立成 chunk（不进主入口）。
//   · echarts/core 按需注册：LineChart + Grid/Tooltip/Legend/DataZoom + CanvasRenderer，不引整包。
//   · 颜色一律取自 antd CSS 变量（cssVar.key=v2link），深浅色切换时重设 option。
//   · 容器 resize 走 ResizeObserver；卸载时 dispose。

echarts.use([
  LineChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
  CanvasRenderer,
])

type EChartsOption = echarts.ComposeOption<
  | LineSeriesOption
  | GridComponentOption
  | TooltipComponentOption
  | LegendComponentOption
  | DataZoomComponentOption
>

// 与 style.css 的响应式命中条件一致：窄屏或触摸设备。
const COARSE_QUERY = '(max-width: 991px), (pointer: coarse)'

// 轴标签字号：桌面维持 10px（保持桌面观感），窄屏 / 触摸提到 12px（可见文本 ≥12px）。
function axisFontSize(): number {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return 10
  return window.matchMedia(COARSE_QUERY).matches ? 12 : 10
}

interface ThemeColors {
  text?: string
  sub?: string
  line?: string
  up?: string
  grid?: string
}

// 从容器解析 antd 的 CSS 变量；缺失返回 undefined，交由 ECharts 默认（不写死浅色值）。
function readColors(el: HTMLElement): ThemeColors {
  const style = getComputedStyle(el)
  const pick = (name: string): string | undefined =>
    style.getPropertyValue(name).trim() || undefined
  return {
    text: pick('--ant-color-text'),
    sub: pick('--ant-color-text-tertiary'),
    line: pick('--ant-color-primary'),
    up: pick('--ant-color-text-quaternary'),
    grid: pick('--ant-color-border-secondary'),
  }
}

function tickLabel(ts: number): string {
  const d = new Date(ts)
  const p = (x: number) => String(x).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:00`
}

function buildOption(points: TrafficPoint[], c: ThemeColors, labelSize: number): EChartsOption {
  const hasUp = points.some((p) => p.up > 0)
  const bytes = (value: unknown) => formatBytes(Number(value))
  return {
    grid: { left: 8, right: 12, top: 28, bottom: 22, containLabel: true },
    legend: {
      top: 0,
      right: 0,
      itemWidth: 12,
      itemHeight: 8,
      textStyle: { color: c.text },
      data: hasUp ? ['下行', '上行'] : ['下行'],
    },
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: points.map((p) => tickLabel(p.ts)),
      axisTick: { show: false },
      axisLine: { lineStyle: { color: c.grid } },
      axisLabel: { color: c.sub, fontSize: labelSize, hideOverlap: true },
    },
    yAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: c.grid } },
      axisLabel: { color: c.sub, fontSize: labelSize, formatter: (v: number) => formatBytes(v) },
    },
    dataZoom: [{ type: 'inside' }],
    series: [
      {
        name: '下行',
        type: 'line',
        smooth: false,
        symbol: 'none',
        data: points.map((p) => p.down),
        lineStyle: { color: c.line, width: 1.6 },
        itemStyle: { color: c.line },
        areaStyle: { color: c.line, opacity: 0.12 },
        tooltip: { valueFormatter: bytes },
      },
      ...(hasUp
        ? [
            {
              name: '上行',
              type: 'line' as const,
              smooth: false,
              symbol: 'none',
              data: points.map((p) => p.up),
              lineStyle: { color: c.up, width: 1.2 },
              itemStyle: { color: c.up },
              tooltip: { valueFormatter: bytes },
            },
          ]
        : []),
    ],
  }
}

function formatChart(n: number): string {
  if (n >= 1024 ** 3) return `${(n / 1024 ** 3).toFixed(1)} GB`
  if (n >= 1024 ** 2) return `${(n / 1024 ** 2).toFixed(1)} MB`
  if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${n} B`
}

export default function TrafficChart({ points }: { points: TrafficPoint[] }) {
  const ref = useRef<HTMLDivElement>(null)
  const chartRef = useRef<echarts.ECharts | null>(null)
  const { resolved } = useThemeMode()
  const peak = useMemo(() => maxVal(points), [points])

  // 初始化 + 尺寸跟随 + 卸载销毁
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const chart = echarts.init(el)
    chartRef.current = chart
    const ro = new ResizeObserver(() => chart.resize())
    ro.observe(el)
    return () => {
      ro.disconnect()
      chart.dispose()
      chartRef.current = null
    }
  }, [])

  // 数据或主题变化时重设 option（颜色重新从 CSS 变量读取）；窄屏/触摸断点变化时同步轴标签字号
  useEffect(() => {
    const chart = chartRef.current
    const el = ref.current
    if (!chart || !el) return
    const apply = () => chart.setOption(buildOption(points, readColors(el), axisFontSize()), true)
    apply()
    const mql =
      typeof window.matchMedia === 'function' ? window.matchMedia(COARSE_QUERY) : null
    mql?.addEventListener('change', apply)
    return () => mql?.removeEventListener('change', apply)
  }, [points, resolved])

  return (
    <div className="traffic-chart">
      <div ref={ref} className="traffic-chart-canvas" role="img" aria-label="流量趋势" />
      {peak > 0 && <span className="chart-peak">峰值 ↓ {formatChart(peak)}</span>}
    </div>
  )
}
