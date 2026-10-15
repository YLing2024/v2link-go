import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

// ClashSubModal：打开即 POST、渲染二维码/订阅链接/一键导入/倒计时，过期提示 + 重新生成。

const { createClashSubscriptionMock } = vi.hoisted(() => ({
  createClashSubscriptionMock: vi.fn(),
}))
vi.mock('../api', () => ({ createClashSubscription: createClashSubscriptionMock }))
vi.mock('qrcode', () => ({
  default: { toDataURL: vi.fn(async () => 'data:image/png;base64,AAA') },
}))

import { ClashSubModal } from './ClashSubModal'
import type { ClashSubResult } from '../types'

function result(over: Partial<ClashSubResult> = {}): ClashSubResult {
  const url = 'https://v2.example.com/api/clash/subscriptions/tok_abc'
  return {
    token: 'tok_abc',
    url,
    importUrl: `clash://install-config?url=${encodeURIComponent(url)}&name=v2link`,
    expiresAt: Date.now() + 600_000,
    ttlSeconds: 600,
    count: 2,
    skipped: [],
    nodes: [],
    ...over,
  }
}

afterEach(() => {
  vi.useRealTimers()
  createClashSubscriptionMock.mockReset()
})

describe('ClashSubModal', () => {
  it('打开即按勾选顺序 POST，并渲染二维码/订阅链接/导入/提示', async () => {
    createClashSubscriptionMock.mockResolvedValue(result())
    render(<ClashSubModal linkIds={['lk_a', 'lk_b']} onClose={() => undefined} />)
    expect(await screen.findByText('复制订阅链接')).toBeInTheDocument()
    expect(createClashSubscriptionMock).toHaveBeenCalledWith(['lk_a', 'lk_b'])
    expect(await screen.findByAltText('订阅二维码')).toHaveAttribute(
      'src',
      'data:image/png;base64,AAA',
    )
    expect(
      screen.getByText('https://v2.example.com/api/clash/subscriptions/tok_abc'),
    ).toBeInTheDocument()
    expect(screen.getByText('复制 clash:// 导入链接')).toBeInTheDocument()
    expect(screen.getByText(/剩余 \d{2}:\d{2}/)).toBeInTheDocument()
    expect(screen.getByText(/订阅本身 10 分钟内有效/)).toBeInTheDocument()
  })

  it('订阅已过期时显示已过期，点「重新生成」再次请求', async () => {
    createClashSubscriptionMock.mockResolvedValue(result({ expiresAt: Date.now() - 1_000 }))
    render(<ClashSubModal linkIds={['lk_a']} onClose={() => undefined} />)
    expect(await screen.findByText('已过期，请重新生成')).toBeInTheDocument()

    createClashSubscriptionMock.mockClear()
    createClashSubscriptionMock.mockResolvedValue(result())
    fireEvent.click(screen.getByText('重新生成'))
    await waitFor(() =>
      expect(createClashSubscriptionMock).toHaveBeenCalledWith(['lk_a']),
    )
  })

  it('显示被忽略的不存在链接数量', async () => {
    createClashSubscriptionMock.mockResolvedValue(result({ skipped: ['lk_x'] }))
    render(<ClashSubModal linkIds={['lk_a']} onClose={() => undefined} />)
    expect(await screen.findByText('已忽略 1 个不存在的链接')).toBeInTheDocument()
  })

  it('请求失败显示错误文案', async () => {
    createClashSubscriptionMock.mockRejectedValue(new Error('生成失败'))
    render(<ClashSubModal linkIds={['lk_a']} onClose={() => undefined} />)
    expect(await screen.findByText('生成失败')).toBeInTheDocument()
  })
})
