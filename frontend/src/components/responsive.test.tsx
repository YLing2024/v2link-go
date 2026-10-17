import { render, screen } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import process from 'node:process'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// 移动端响应式机检（对应需求书 §3）。
// jsdom 不做真实布局、也不应用媒体查询，无法量 390×844 的像素，因此按需求书给出的降级口径断言：
//   · 表格横滑 → 断言 DOM 结构：antd 在 scroll.x 下给表体内联 `overflow-x:auto`、table `width:max-content`，
//     且滚动容器到 wrapper 的祖先链上没有 `overflow:hidden` 裁剪（表头克隆 .ant-table-header 是兄弟节点，不计入）。
//   · 触摸目标 / 字号 → 断言响应式样式表规则（min-height / font-size）。样式表只在
//     `max-width:991px` 或 `pointer:coarse` 下生效，桌面（≥992px 且精细指针）不进入本段。

const { listLinksMock, auditLogMock, linkTrafficMock, linkConnectionsMock } = vi.hoisted(() => ({
  listLinksMock: vi.fn(),
  auditLogMock: vi.fn(),
  linkTrafficMock: vi.fn(),
  linkConnectionsMock: vi.fn(),
}))

vi.mock('../api', () => ({
  listLinks: listLinksMock,
  logout: vi.fn(),
  revokeLink: vi.fn(),
  deleteLink: vi.fn(),
  createLink: vi.fn(),
  extendLink: vi.fn(),
  linkTraffic: linkTrafficMock,
  linkConnections: linkConnectionsMock,
  auditLog: auditLogMock,
  createClashSubscription: vi.fn(),
  regionProbes: vi.fn(async () => ({ updatedAt: 0, probes: [], history: [] })),
}))
vi.mock('qrcode', () => ({
  default: { toDataURL: vi.fn(async () => 'data:image/png;base64,AAA') },
}))

import Dashboard from './Dashboard'
import { AuditModal } from './AuditModal'
import { LinkDetailModal } from './LinkDetailModal'
import type { AuditRecord, ConnectionRecord, Link } from '../types'

// 直接读样式表源文本：jsdom 不解析外部 CSS，Vitest 又会把 CSS 导入 stub 成空串，
// 故用 node:fs 读源文件，机检响应式规则（字号 / 触摸目标 / 间距）。
const rawCss = readFileSync(resolve(process.cwd(), 'src/styles/style.css'), 'utf8')

function link(id: string, note: string): Link {
  return {
    id,
    uuid: `uuid-${id}`,
    note,
    alias: '',
    upBytes: 0,
    downBytes: 0,
    createdAt: Date.now(),
    expiresAt: Date.now() + 3600_000,
    permanent: false,
    revokedAt: null,
    status: 'active',
  }
}

function auditRow(): AuditRecord {
  return {
    id: 1,
    ts: Date.now(),
    actor: 'operator',
    action: 'create',
    link_id: 'lk_a',
    detail: { note: '示例' },
  }
}

function connRow(): ConnectionRecord {
  return {
    id: 1,
    link_id: 'lk_a',
    email: 'user@example.com',
    ts: Date.now(),
    host: 'example.com',
    port: 443,
    up_bytes: 1024,
    down_bytes: 2048,
    duration_ms: 1200,
  }
}

// 断言每张表格都有可横滑的表体（scroll.x 生效），且滚动容器祖先链无 overflow:hidden 裁剪。
function expectScrollableTable(root: ParentNode): void {
  const wrappers = root.querySelectorAll('.ant-table-wrapper')
  expect(wrappers.length, '至少渲染一张表格').toBeGreaterThan(0)
  wrappers.forEach((wrapper) => {
    const scroller =
      wrapper.querySelector<HTMLElement>('.ant-table-body') ??
      wrapper.querySelector<HTMLElement>('.ant-table-content')
    expect(scroller, '表体滚动容器存在').not.toBeNull()
    expect(scroller!.style.overflowX, '表体横向可滚动').toBe('auto')
    const table = scroller!.querySelector('table')
    expect(table, '表体 table 存在').not.toBeNull()
    expect(table!.style.width, '表格宽度由内容撑开').toBe('max-content')

    // 判据看表体：从滚动容器向上到 wrapper，链上不得有内联 overflow:hidden 裁剪。
    let el: HTMLElement | null = scroller
    while (el) {
      expect(el.style.overflow, `${el.className} 无 overflow:hidden`).not.toBe('hidden')
      expect(el.style.overflowX, `${el.className} 无 overflow-x:hidden`).not.toBe('hidden')
      if (el === wrapper) break
      el = el.parentElement
    }
  })
}

beforeEach(() => {
  listLinksMock.mockReset()
  auditLogMock.mockReset()
  linkTrafficMock.mockReset()
  linkConnectionsMock.mockReset()
})

describe('响应式 · 表格横滑', () => {
  it('Dashboard 链接表可横滑', async () => {
    listLinksMock.mockResolvedValue([link('lk_a', '节点甲')])
    render(<Dashboard />)
    await screen.findByText('节点甲')
    expectScrollableTable(document.body)
  })

  it('审计弹窗表格可横滑', async () => {
    auditLogMock.mockResolvedValue({ rows: [auditRow()], total: 1, limit: 30, offset: 0 })
    render(<AuditModal onClose={() => undefined} />)
    await screen.findByText('操作审计')
    expectScrollableTable(document.body)
  })

  it('连接记录弹窗表格可横滑', async () => {
    linkTrafficMock.mockResolvedValue([])
    linkConnectionsMock.mockResolvedValue({
      rows: [connRow()],
      total: 1,
      limit: 30,
      offset: 0,
    })
    render(<LinkDetailModal link={link('lk_a', '节点甲')} onClose={() => undefined} />)
    await screen.findByText('连接记录')
    expectScrollableTable(document.body)
  })
})

describe('响应式 · 字号与触摸目标样式表', () => {
  // 取出样式表里按 991px / coarse 命中的响应式块，机检其中的字号与尺寸阈值。
  function responsiveBlock(css: string): string {
    const header = '@media (max-width: 991px), (pointer: coarse)'
    const start = css.indexOf(header)
    expect(start, '存在窄屏/触摸响应式块').toBeGreaterThan(-1)
    const open = css.indexOf('{', start)
    let depth = 0
    for (let i = open; i < css.length; i++) {
      if (css[i] === '{') depth++
      else if (css[i] === '}') {
        depth--
        if (depth === 0) return css.slice(open + 1, i)
      }
    }
    throw new Error('响应式块括号不配平')
  }

  const block = responsiveBlock(rawCss)

  it('桌面基准字号保持 <12px（≥992px 观感不漂）', () => {
    expect(rawCss).toMatch(/\.mono\s*\{[^}]*font-size:\s*11\.5px/)
  })

  it('响应式块把 <12px 文本类抬到 ≥12px', () => {
    for (const sel of ['.mono', '.cell-sub', '.vless-uri', '.chart-peak']) {
      const re = new RegExp(`\\${sel}\\s*\\{[^}]*font-size:\\s*(1[2-9]|[2-9]\\d)px`)
      expect(block, `${sel} 字号 ≥12px`).toMatch(re)
    }
  })

  it('响应式块把主要操作按钮抬到 ≥44px、关闭按钮 ≥32×32', () => {
    expect(block).toMatch(/\.ant-btn\s*\{[^}]*min-height:\s*(4[4-9]|[5-9]\d)px/)
    expect(block).toMatch(/\.ant-modal-close[^{]*\{[^}]*min-width:\s*32px/)
    expect(block).toMatch(/\.ant-modal-close[^{]*\{[^}]*min-height:\s*32px/)
  })

  it('响应式块把表单输入抬到 ≥44px', () => {
    expect(block).toMatch(/\.ant-input[^{]*\{[^}]*min-height:\s*(4[4-9]|[5-9]\d)px/)
  })

  it('响应式块为 <8px 的 Space 间距提供 ≥8px 兜底', () => {
    expect(block).toMatch(/\.touch-gap\s*\{[^}]*column-gap:\s*8px\s*!important/)
  })

  it('渲染出的表格文本实际使用被抬升的字号类', async () => {
    listLinksMock.mockResolvedValue([link('lk_a', '节点甲')])
    const { container } = render(<Dashboard />)
    await screen.findByText('节点甲')
    expect(container.querySelector('.mono')).not.toBeNull()
    expect(container.querySelector('.cell-sub')).not.toBeNull()
  })
})
