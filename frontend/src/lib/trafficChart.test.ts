import { describe, expect, it } from 'vitest'
import { chartGeom, maxVal } from '../lib/trafficChart'
import type { TrafficPoint } from '../types'

describe('maxVal', () => {
  it('取 up/down 最大', () => {
    const pts: TrafficPoint[] = [
      { ts: 1, up: 5, down: 10 },
      { ts: 2, up: 0, down: 2 },
    ]
    expect(maxVal(pts)).toBe(10)
  })
  it('空 / 全零', () => {
    expect(maxVal([])).toBe(0)
    expect(maxVal([{ ts: 1, up: 0, down: 0 }])).toBe(0)
  })
})

describe('chartGeom', () => {
  it('生成连续 path（平滑起点 M + 贝塞尔 C），下行面积收口底边', () => {
    const pts: TrafficPoint[] = [
      { ts: 1, up: 0, down: 0 },
      { ts: 2, up: 10, down: 20 },
      { ts: 3, up: 5, down: 8 },
    ]
    const g = chartGeom(pts)
    expect(g.down).toContain('M ')
    expect(g.up).toContain('M ')
    expect(g.area.endsWith('Z')).toBe(true)
    expect(g.max).toBe(20)
  })

  it('空 / 全零 / 单点 → 折线为空串（父级渲染空态）', () => {
    expect(chartGeom([]).down).toBe('')
    expect(chartGeom([{ ts: 1, up: 0, down: 0 }]).down).toBe('')
    expect(
      chartGeom([
        { ts: 1, up: 0, down: 0 },
        { ts: 2, up: 0, down: 0 },
      ]).down,
    ).toBe('')
  })

  it('上行全零但下行有值 → 只出下行', () => {
    const g = chartGeom([
      { ts: 1, up: 0, down: 5 },
      { ts: 2, up: 0, down: 9 },
    ])
    expect(g.down).toContain('M ')
    expect(g.up).toBe('')
  })
})
