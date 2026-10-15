import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

// 「生成链接」弹窗：**过期时刻（分钟精度）为主输入**，小时档只是快捷填充（用户 2026-09-14 反馈）。
// 断言落在 API 调用参数上（组件行为契约），不依赖后端。
const { createLinkMock } = vi.hoisted(() => ({
  createLinkMock: vi.fn((_body: Record<string, unknown>) => Promise.resolve({} as never)),
}))
vi.mock('../api', () => ({ createLink: createLinkMock }))

import { CreateModal } from './CreateModal'

function setup() {
  const onCreated = vi.fn()
  const onClose = vi.fn()
  render(<CreateModal onClose={onClose} onCreated={onCreated} />)
  return { onCreated, onClose }
}

function bodyOf(call = 0): Record<string, unknown> {
  return createLinkMock.mock.calls[call]![0] as Record<string, unknown>
}

function expiryInput(): HTMLInputElement {
  return document.querySelector('.ant-modal input[type="datetime-local"]') as HTMLInputElement
}

function setLocalDatetime(ms: number): string {
  const d = new Date(ms)
  const p = (x: number) => String(x).padStart(2, '0')
  const v = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
  fireEvent.change(expiryInput(), { target: { value: v } })
  return v
}

describe('CreateModal（过期时刻为主）', () => {
  it('默认预填 24 小时后并提交 expiresAt（不是 hours）', async () => {
    createLinkMock.mockClear()
    setup()
    expect(expiryInput().value).not.toBe('')
    fireEvent.click(screen.getByText('生成'))
    await waitFor(() => expect(createLinkMock).toHaveBeenCalledTimes(1))
    const body = bodyOf()
    expect(typeof body.expiresAt).toBe('number')
    expect(body.hours).toBeUndefined()
    const diffH = ((body.expiresAt as number) - Date.now()) / 3600_000
    expect(diffH).toBeGreaterThan(23.9)
    expect(diffH).toBeLessThan(24.1)
  })

  it('快捷档「1 小时」→ 把时刻设到 ~1 小时后', async () => {
    createLinkMock.mockClear()
    setup()
    fireEvent.click(screen.getByText('1 小时'))
    fireEvent.click(screen.getByText('生成'))
    await waitFor(() => expect(createLinkMock).toHaveBeenCalledTimes(1))
    const diffH = ((bodyOf().expiresAt as number) - Date.now()) / 3600_000
    expect(diffH).toBeGreaterThan(0.9)
    expect(diffH).toBeLessThan(1.1)
  })

  it('手改时刻 → 按输入值提交（分钟精度）', async () => {
    createLinkMock.mockClear()
    setup()
    const target = Date.now() + 47 * 60_000
    const typed = setLocalDatetime(target)
    fireEvent.click(screen.getByText('生成'))
    await waitFor(() => expect(createLinkMock).toHaveBeenCalledTimes(1))
    expect(bodyOf().expiresAt).toBe(new Date(typed).getTime())
  })

  it('选「永久」→ 提交 permanent: true（隐藏时刻输入）', async () => {
    createLinkMock.mockClear()
    setup()
    fireEvent.click(screen.getByText('永久'))
    expect(document.querySelector('.ant-modal input[type="datetime-local"]')).toBeNull()
    expect(screen.getByText(/永久链接不会自动过期/)).toBeInTheDocument()
    fireEvent.click(screen.getByText('生成'))
    await waitFor(() => expect(createLinkMock).toHaveBeenCalledTimes(1))
    expect(bodyOf()).toMatchObject({ permanent: true })
    expect(bodyOf().expiresAt).toBeUndefined()
  })

  it('填别名 → 提交带 alias；过去时刻 → 拒绝提交', async () => {
    createLinkMock.mockClear()
    setup()
    fireEvent.change(screen.getByPlaceholderText(/客户端里显示的节点名/), {
      target: { value: '我的节点' },
    })
    setLocalDatetime(Date.now() - 3600_000)
    fireEvent.click(screen.getByText('生成'))
    await waitFor(() =>
      expect(document.querySelector('.ant-alert-message')?.textContent).toMatch(/须晚于当前时间/),
    )
    expect(createLinkMock).not.toHaveBeenCalled()

    setLocalDatetime(Date.now() + 3600_000)
    fireEvent.click(screen.getByText('生成'))
    await waitFor(() => expect(createLinkMock).toHaveBeenCalledTimes(1))
    expect(bodyOf()).toMatchObject({ alias: '我的节点' })
  })
})
