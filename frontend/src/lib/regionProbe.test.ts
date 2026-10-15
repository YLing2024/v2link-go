import { describe, expect, it } from 'vitest'
import { probeLabel, toneLabel, toneOf } from './regionProbe'
import type { RegionProbeResult } from '../types'

// 地区连通性展示辅助（地区连通性监控）阈值语义：
//   绿 ok <300ms；黄 ok 300~800ms；红 ok>800 / 失败 / 无数据。

function res(p: Partial<RegionProbeResult>): RegionProbeResult {
  return { key: 'us', ok: true, rttMs: 100, error: null, ts: 0, ...p }
}

describe('toneOf', () => {
  it('无数据 → red', () => {
    expect(toneOf(undefined)).toBe('red')
  })

  it('ok && rtt <300 → green', () => {
    expect(toneOf(res({ rttMs: 0 }))).toBe('green')
    expect(toneOf(res({ rttMs: 299 }))).toBe('green')
  })

  it('ok && rtt 300~800 → yellow', () => {
    expect(toneOf(res({ rttMs: 300 }))).toBe('yellow')
    expect(toneOf(res({ rttMs: 800 }))).toBe('yellow')
  })

  it('ok && rtt >800 → red', () => {
    expect(toneOf(res({ rttMs: 801 }))).toBe('red')
  })

  it('!ok / rtt null → red', () => {
    expect(toneOf(res({ ok: false }))).toBe('red')
    expect(toneOf(res({ rttMs: null }))).toBe('red')
  })
})

describe('probeLabel', () => {
  it('ok 显示 rtt 毫秒', () => {
    expect(probeLabel(res({ rttMs: 120 }))).toBe('120ms')
  })

  it('失败显示 error；无数据显示 —', () => {
    expect(probeLabel(res({ ok: false, error: 'timeout' }))).toBe('timeout')
    expect(probeLabel(undefined)).toBe('—')
  })
})

describe('toneLabel', () => {
  it('中文标签', () => {
    expect(toneLabel('green')).toBe('正常')
    expect(toneLabel('yellow')).toBe('偏慢')
    expect(toneLabel('red')).toBe('不可达')
  })
})
