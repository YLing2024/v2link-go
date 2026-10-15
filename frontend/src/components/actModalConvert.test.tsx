import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { Link } from '../types'

// 「编辑过期时间」弹窗（用户 2026-09-14 反馈）：直接改过期时刻，不再是「延长 N 小时」。
//   · 限时链接：预填当前到期时刻；可改时刻，也可「转为永久」
//   · 永久链接：无到期时刻可预填，选时刻即转为限时
const { extendLinkMock } = vi.hoisted(() => ({
  extendLinkMock: vi.fn((_id: string, _body: Record<string, unknown>) => Promise.resolve({} as never)),
}))
vi.mock('../api', () => ({ extendLink: extendLinkMock }))

import { ActModal } from './ActModal'

function makeLink(over: Partial<Link> = {}): Link {
  return {
    id: 'lk_test',
    uuid: '00000000-0000-0000-0000-000000000000',
    note: '',
    alias: '',
    upBytes: 0,
    downBytes: 0,
    createdAt: Date.now(),
    expiresAt: Date.now() + 3600_000,
    permanent: false,
    revokedAt: null,
    status: 'active',
    ...over,
  }
}

function setup(link: Link) {
  const onDone = vi.fn()
  const onClose = vi.fn()
  render(<ActModal link={link} onClose={onClose} onDone={onDone} />)
  return { onDone, onClose }
}

// antd 迁移：原 .chip 快捷档现为 antd Button；用可访问名判定「方式」两档是否存在
const hasMode = (name: string) => screen.queryByRole('button', { name }) !== null

function setLocalDatetime(ms: number) {
  const d = new Date(ms)
  const p = (x: number) => String(x).padStart(2, '0')
  const v = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
  const input = document.querySelector(
    '.ant-modal input[type="datetime-local"]',
  ) as HTMLInputElement
  fireEvent.change(input, { target: { value: v } })
  return v
}

describe('编辑过期时间（限时链接）', () => {
  it('预填当前到期时刻，改时刻后提交 { expiresAt }', async () => {
    extendLinkMock.mockClear()
    const link = makeLink({ expiresAt: Date.now() + 3600_000 })
    setup(link)
    expect(hasMode('指定时刻')).toBe(true)
    expect(hasMode('转为永久')).toBe(true)
    const input = document.querySelector(
      '.ant-modal input[type="datetime-local"]',
    ) as HTMLInputElement
    expect(input.value).not.toBe('')
    const target = Date.now() + 5 * 3600_000
    const typed = setLocalDatetime(target)
    expect(screen.getByText(/距现在约/)).toBeInTheDocument()

    fireEvent.click(screen.getByText('确定'))
    await waitFor(() => expect(extendLinkMock).toHaveBeenCalledTimes(1))
    const body = extendLinkMock.mock.calls[0]![1] as Record<string, unknown>
    expect(typeof body.expiresAt).toBe('number')
    // 提交值 = 输入框解析结果（分钟精度）
    expect(new Date(typed).getTime()).toBe(body.expiresAt)
  })

  it('改为更早时刻 → 二次确认；取消则不提交', async () => {
    extendLinkMock.mockClear()
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    setup(makeLink({ expiresAt: Date.now() + 10 * 3600_000 }))
    setLocalDatetime(Date.now() + 3600_000)
    fireEvent.click(screen.getByText('确定'))
    await new Promise((r) => setTimeout(r, 30))
    expect(confirmSpy).toHaveBeenCalled()
    expect(extendLinkMock).not.toHaveBeenCalled()
  })

  it('转为永久 → confirm 后提交 { permanent: true }', async () => {
    extendLinkMock.mockClear()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    setup(makeLink())
    fireEvent.click(screen.getByText('转为永久'))
    expect(screen.getByText(/转为永久后不再自动过期/)).toBeInTheDocument()
    fireEvent.click(screen.getByText('确定'))
    await waitFor(() => expect(extendLinkMock).toHaveBeenCalledTimes(1))
    expect(extendLinkMock.mock.calls[0]![1]).toMatchObject({ permanent: true })
  })
})

describe('编辑过期时间（永久链接）', () => {
  it('无方式切换、输入为空，提示「选择过期时间即转为限时」', () => {
    setup(makeLink({ permanent: true, expiresAt: 0 }))
    expect(hasMode('指定时刻')).toBe(false)
    expect(hasMode('转为永久')).toBe(false)
    expect(screen.getByText(/选择过期时间即转为限时/)).toBeInTheDocument()
    const input = document.querySelector(
      '.ant-modal input[type="datetime-local"]',
    ) as HTMLInputElement
    expect(input.value).toBe('')
  })

  it('填未来时刻 → 提交 { expiresAt }（转限时）', async () => {
    extendLinkMock.mockClear()
    setup(makeLink({ permanent: true, expiresAt: 0 }))
    setLocalDatetime(Date.now() + 3 * 3600_000)
    fireEvent.click(screen.getByText('确定'))
    await waitFor(() => expect(extendLinkMock).toHaveBeenCalledTimes(1))
    const body = extendLinkMock.mock.calls[0]![1] as Record<string, unknown>
    expect(typeof body.expiresAt).toBe('number')
    expect(body.permanent).toBeUndefined()
  })

  it('过去时刻 → 报错且不提交', async () => {
    extendLinkMock.mockClear()
    setup(makeLink({ permanent: true, expiresAt: 0 }))
    setLocalDatetime(Date.now() - 3600_000)
    fireEvent.click(screen.getByText('确定'))
    await waitFor(() =>
      expect(document.querySelector('.ant-alert-message')?.textContent).toMatch(/须晚于当前时间/),
    )
    expect(extendLinkMock).not.toHaveBeenCalled()
  })
})
