import { describe, expect, it } from 'vitest'
import {
  formatBytes,
  formatCountdown,
  formatDateTime,
  formatExpiry,
  formatRelative,
  formatRemaining,
  humanDuration,
  isPermanentExpiry,
} from './format'

describe('formatBytes', () => {
  it('B 级', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(512)).toBe('512 B')
  })
  it('KB', () => {
    expect(formatBytes(1024)).toBe('1.0 KB')
  })
  it('MB', () => {
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.0 MB')
  })
  it('GB', () => {
    expect(formatBytes(2.5 * 1024 * 1024 * 1024)).toBe('2.5 GB')
  })
  it('非法值容错', () => {
    expect(formatBytes(-1)).toBe('0 B')
    expect(formatBytes(NaN)).toBe('0 B')
  })
})

describe('formatRelative', () => {
  const now = 1_800_000_000_000
  it('刚刚', () => {
    expect(formatRelative(now - 1000, now)).toBe('刚刚')
  })
  it('分钟/小时/天', () => {
    expect(formatRelative(now - 5 * 60_000, now)).toBe('5 分钟前')
    expect(formatRelative(now - 3 * 3600_000, now)).toBe('3 小时前')
    expect(formatRelative(now - 2 * 86400_000, now)).toBe('2 天前')
  })
  it('未来', () => {
    expect(formatRelative(now + 2 * 3600_000, now)).toBe('2 小时后')
  })
})

describe('formatDateTime', () => {
  it('格式 YYYY-MM-DD HH:mm（本地时区）', () => {
    const ts = new Date(2024, 0, 5, 9, 8).getTime()
    expect(formatDateTime(ts)).toBe('2024-01-05 09:08')
  })
})

describe('永久链接（expires_at 哨兵 0）', () => {
  it('isPermanentExpiry 仅认 0', () => {
    expect(isPermanentExpiry(0)).toBe(true)
    expect(isPermanentExpiry(1)).toBe(false)
    expect(isPermanentExpiry(Date.now())).toBe(false)
  })

  it('formatExpiry：永久 → 「永久」，否则日期时间', () => {
    expect(formatExpiry(0)).toBe('永久')
    const ts = new Date(2024, 0, 5, 9, 8).getTime()
    expect(formatExpiry(ts)).toBe('2024-01-05 09:08')
  })
})

// 剩余时长（派生显示，用户 2026-09-14 反馈：小时/分钟信息放在过期时间旁边）
describe('formatRemaining / humanDuration', () => {
  const now = 1_800_000_000_000
  const min = 60_000

  it('humanDuration：分 / 小时 / 天，<1 分记「不足 1 分」', () => {
    expect(humanDuration(30_000)).toBe('不足 1 分')
    expect(humanDuration(45 * min)).toBe('45 分')
    expect(humanDuration(3 * 3600_000 + 20 * min)).toBe('3 小时 20 分')
    expect(humanDuration(2 * 24 * 3600_000 + 5 * 3600_000)).toBe('2 天 5 小时')
    expect(humanDuration(3 * 24 * 3600_000)).toBe('3 天')
  })

  it('formatRemaining：剩 X / 已过期 X / 永久', () => {
    expect(formatRemaining(now + 45 * min, now)).toBe('剩 45 分')
    expect(formatRemaining(now + 3 * 3600_000 + 20 * min, now)).toBe('剩 3 小时 20 分')
    expect(formatRemaining(now - 2 * 3600_000, now)).toBe('已过期 2 小时')
    expect(formatRemaining(0, now)).toBe('永久')
  })
})

describe('formatCountdown（Clash 订阅倒计时）', () => {
  it('MM:SS；负数按 0', () => {
    expect(formatCountdown(598_000)).toBe('09:58')
    expect(formatCountdown(600_000)).toBe('10:00')
    expect(formatCountdown(5_000)).toBe('00:05')
    expect(formatCountdown(-1)).toBe('00:00')
  })
})
