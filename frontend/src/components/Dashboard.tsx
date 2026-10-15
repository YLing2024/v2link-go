import { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, Button, Drawer, Empty, Layout, Space, Table, Tag, Typography } from 'antd'
import type { CheckboxProps, TableColumnsType, TableProps } from 'antd'
import { deleteLink, listLinks, logout, revokeLink } from '../api'
import { formatBytes, formatDateTime, formatExpiry, formatRemaining } from '../lib/format'
import type { Link } from '../types'
import { CreateModal } from './CreateModal'
import { CopyModal } from './CopyModal'
import { ActModal } from './ActModal'
import { LinkDetailModal } from './LinkDetailModal'
import { AuditModal } from './AuditModal'
import { ClashSubModal } from './ClashSubModal'
import { RegionProbeBar } from './RegionProbeBar'
import { ThemeSwitch } from './ThemeSwitch'

// 主界面：antd Layout（顶栏 + 内容），顶栏右侧常驻主题三态切换。
// 链接列表用 antd Table：列宽明确、操作列 fixed right（配 scroll.x）、表头 sticky（顶栏高 64）。
// 状态列用 Tag，行内操作用 Button type=link size=small；空态用 Empty。
// 不引入分页/排序/筛选，保持原有平铺语义与全部交互文案不变。
// 窄屏（< 768px）把操作收进抽屉，避免挤坏布局。

const STATUS_LABEL: Record<Link['status'], string> = {
  active: '使用中',
  expired: '已过期',
  revoked: '已吊销',
}

// 状态色：使用中=成功绿；已过期 / 已吊销=中性灰（与原「灰点」语义一致）。
const STATUS_COLOR: Record<Link['status'], string> = {
  active: 'success',
  expired: 'default',
  revoked: 'default',
}

type ModalState =
  | { kind: 'create' }
  | { kind: 'copy'; link: Link }
  | { kind: 'extend'; link: Link }
  | { kind: 'detail'; link: Link }
  | { kind: 'audit' }
  | { kind: 'clash' }
  | null

export default function Dashboard() {
  const [links, setLinks] = useState<Link[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [modal, setModal] = useState<ModalState>(null)
  const [busy, setBusy] = useState<string>('') // 正在操作的行 id
  const [navOpen, setNavOpen] = useState(false)
  // 多选态（Clash 订阅用）：Set 保持勾选顺序（JS Set 迭代 = 插入顺序）。
  // 只放 state、不塞进链接对象；关闭弹窗后保留勾选。
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const selectedIds = useMemo(() => [...selected], [selected])

  const refresh = useCallback(async () => {
    setError('')
    try {
      const data = await listLinks()
      setLinks(data)
      // 列表变化后收窄勾选：移除已删除的 id（删除功能会真正删掉链接，避免勾选计数失真）
      const ids = new Set(data.map((l) => l.id))
      setSelected((prev) => {
        const next = new Set([...prev].filter((id) => ids.has(id)))
        return next.size === prev.size ? prev : next
      })
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  function applyUpdate(updated: Link) {
    setLinks((prev) => prev.map((l) => (l.id === updated.id ? updated : l)))
  }

  function selectAll() {
    setSelected(new Set(links.map((l) => l.id)))
  }

  async function doRevoke(link: Link) {
    if (!window.confirm(`吊销 ${link.note || link.id}？吊销后不可恢复。`)) return
    setBusy(link.id)
    try {
      applyUpdate(await revokeLink(link.id))
    } catch (e) {
      window.alert((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  async function doDelete(link: Link) {
    const name = link.alias || link.note || link.id
    const msg =
      link.status === 'active'
        ? `删除「${name}」？会同时断开其正在使用的连接，且不可恢复。`
        : `删除「${name}」？删除后不再出现在列表中。`
    if (!window.confirm(msg)) return
    setBusy(link.id)
    try {
      await deleteLink(link.id)
      await refresh()
    } catch (e) {
      window.alert((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  // 顶栏操作：窄屏抽屉与宽屏内联共用同一组按钮（文案与状态完全一致）
  function renderActions(block: boolean) {
    return (
      <Space
        direction={block ? 'vertical' : 'horizontal'}
        size={block ? 8 : 8}
        wrap={!block}
        style={block ? { width: '100%' } : undefined}
      >
        <Button
          autoInsertSpace={false}
          block={block}
          onClick={() => void refresh()}
          disabled={loading}
        >
          刷新
        </Button>
        <Button autoInsertSpace={false} block={block} onClick={() => setModal({ kind: 'audit' })}>
          审计
        </Button>
        <Button
          autoInsertSpace={false}
          block={block}
          onClick={() => setModal({ kind: 'clash' })}
          disabled={selected.size === 0}
        >
          生成 Clash 订阅
        </Button>
        <Button
          autoInsertSpace={false}
          block={block}
          type="primary"
          onClick={() => setModal({ kind: 'create' })}
        >
          生成链接
        </Button>
        <Button autoInsertSpace={false} block={block} type="text" onClick={logout}>
          退出
        </Button>
      </Space>
    )
  }

  const columns: TableColumnsType<Link> = [
    {
      title: '备注',
      key: 'note',
      width: 280,
      render: (_, l) => (
        <>
          <span>{l.note || '—'}</span>
          <span className="mono cell-sub">{l.id}</span>
          {l.alias && <span className="mono cell-sub">别名：{l.alias}</span>}
        </>
      ),
    },
    {
      title: '状态',
      key: 'status',
      width: 100,
      render: (_, l) => <Tag color={STATUS_COLOR[l.status]}>{STATUS_LABEL[l.status]}</Tag>,
    },
    {
      title: '创建',
      key: 'createdAt',
      width: 170,
      render: (_, l) => <span className="mono">{formatDateTime(l.createdAt)}</span>,
    },
    {
      title: '过期时间',
      key: 'expiresAt',
      width: 190,
      render: (_, l) => (
        <>
          <span className="mono">{formatExpiry(l.expiresAt)}</span>
          <span className="mono cell-sub">{formatRemaining(l.expiresAt)}</span>
        </>
      ),
    },
    {
      title: '流量 ↓ / ↑',
      key: 'traffic',
      width: 180,
      align: 'right',
      render: (_, l) => (
        <span className="traffic-cell">
          <span className="down">↓ {formatBytes(l.downBytes)}</span>{' '}
          <span className="up">↑ {formatBytes(l.upBytes)}</span>
        </span>
      ),
    },
    {
      title: '操作',
      key: 'ops',
      width: 300,
      fixed: 'right',
      align: 'right',
      render: (_, l) => {
        const active = l.status === 'active'
        const isBusy = busy === l.id
        return (
          <Space size={4}>
            <Button
              autoInsertSpace={false}
              type="link"
              size="small"
              onClick={() => setModal({ kind: 'detail', link: l })}
            >
              详情
            </Button>
            <Button
              autoInsertSpace={false}
              type="link"
              size="small"
              onClick={() => setModal({ kind: 'copy', link: l })}
            >
              复制/二维码
            </Button>
            {active && (
              <>
                <Button
                  autoInsertSpace={false}
                  type="link"
                  size="small"
                  onClick={() => setModal({ kind: 'extend', link: l })}
                >
                  编辑
                </Button>
                <Button
                  autoInsertSpace={false}
                  type="link"
                  size="small"
                  danger
                  onClick={() => void doRevoke(l)}
                  disabled={isBusy}
                >
                  吊销
                </Button>
              </>
            )}
            <Button
              autoInsertSpace={false}
              type="link"
              size="small"
              danger
              onClick={() => void doDelete(l)}
              disabled={isBusy}
            >
              删除
            </Button>
          </Space>
        )
      },
    },
  ]

  const rowSelection: TableProps<Link>['rowSelection'] = {
    columnWidth: 48,
    selectedRowKeys: selectedIds,
    onChange: (keys) => setSelected(new Set(keys.map(String))),
    getCheckboxProps: (record) =>
      ({ 'aria-label': `选择 ${record.note || record.id}` }) as Partial<CheckboxProps>,
  }

  return (
    <Layout className="app-shell">
      <Layout.Header className="app-header">
        <div className="app-brand">
          <Typography.Text strong className="app-brand-name">
            v2link
          </Typography.Text>
          <Typography.Text type="secondary" className="app-brand-sub">
            临时 VLESS 链接
          </Typography.Text>
        </div>

        <div className="app-actions">
          <ThemeSwitch />
          {renderActions(false)}
        </div>
        <div className="app-menu">
          <ThemeSwitch />
          <Button autoInsertSpace={false} onClick={() => setNavOpen(true)}>
            菜单
          </Button>
        </div>
      </Layout.Header>

      <Layout.Content className="app-content">
        {error && <Alert className="app-banner" type="error" showIcon message={error} />}

        <Space className="app-toolbar" size={8} wrap>
          <Typography.Text type="secondary">已选 {selected.size} 项</Typography.Text>
          <Button
            autoInsertSpace={false}
            size="small"
            onClick={selectAll}
            disabled={links.length === 0}
          >
            全选
          </Button>
          <Button
            autoInsertSpace={false}
            size="small"
            onClick={() => setSelected(new Set())}
            disabled={selected.size === 0}
          >
            清空
          </Button>
        </Space>

        <Table<Link>
          className="link-table"
          rowKey="id"
          columns={columns}
          dataSource={links}
          rowSelection={rowSelection}
          pagination={false}
          scroll={{ x: 'max-content' }}
          sticky={{ offsetHeader: 64 }}
          rowClassName={(l) => (l.status === 'active' ? '' : 'row-dim')}
          locale={{
            emptyText: loading ? (
              <span>加载中…</span>
            ) : (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无链接" />
            ),
          }}
        />
      </Layout.Content>

      <RegionProbeBar />

      <Drawer
        title="v2link"
        placement="right"
        width={260}
        open={navOpen}
        onClose={() => setNavOpen(false)}
      >
        <Space direction="vertical" size={16} style={{ width: '100%' }}>
          <ThemeSwitch block />
          {renderActions(true)}
        </Space>
      </Drawer>

      {modal?.kind === 'create' && (
        <CreateModal onClose={() => setModal(null)} onCreated={() => void refresh()} />
      )}
      {modal?.kind === 'copy' && <CopyModal link={modal.link} onClose={() => setModal(null)} />}
      {modal?.kind === 'extend' && (
        <ActModal link={modal.link} onClose={() => setModal(null)} onDone={applyUpdate} />
      )}
      {modal?.kind === 'detail' && (
        <LinkDetailModal link={modal.link} onClose={() => setModal(null)} />
      )}
      {modal?.kind === 'audit' && <AuditModal onClose={() => setModal(null)} />}
      {modal?.kind === 'clash' && (
        <ClashSubModal linkIds={selectedIds} onClose={() => setModal(null)} />
      )}
    </Layout>
  )
}
