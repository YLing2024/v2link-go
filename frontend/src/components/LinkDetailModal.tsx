import { useEffect, useMemo, useState } from 'react'
import { Alert, Button, Empty, Input, Space, Table, Typography } from 'antd'
import type { TableColumnsType } from 'antd'
import { linkConnections, linkTraffic } from '../api'
import type { ConnectionRecord, Link, TrafficPoint } from '../types'
import Modal from './Modal'
import TrafficChart from './TrafficChart'

// 单链接追溯视图（监控 + B §3）：
//   顶部「流量趋势」：近 24h / 近 7d / 近 30d 三个快捷档（bucket 随之 hour/day）
//   底部「连接记录」：时间/目标域名/端口/用户搜索，分页
// 数据量大走接口分页；曲线量小一次拉完。
// 本轮改 antd：Modal/Button/Space/Input/Table/Typography/Empty。表格加 sticky 表头；
// 表格加 scroll.x：窄屏可横向滚动，避免右侧列（用户）被裁切。
// 分页沿用原「N 条 · 第 x/y 页」文案与按钮顺序（不换成 antd Pagination，避免改文案）。

type Range = { label: string; hours: number; bucket: 'hour' | 'day' }

const RANGES: Range[] = [
  { label: '24h', hours: 24, bucket: 'hour' },
  { label: '7d', hours: 24 * 7, bucket: 'day' },
  { label: '30d', hours: 24 * 30, bucket: 'day' },
]

export function LinkDetailModal({
  link,
  onClose,
}: {
  link: Link
  onClose: () => void
}) {
  const [range, setRange] = useState<Range>(RANGES[0]!)
  const [points, setPoints] = useState<TrafficPoint[] | null>(null)
  const [chartErr, setChartErr] = useState('')

  const [conns, setConns] = useState<ConnectionRecord[]>([])
  const [connTotal, setConnTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [q, setQ] = useState('')
  const [searchInput, setSearchInput] = useState('')
  const [connLoading, setConnLoading] = useState(false)
  const [connErr, setConnErr] = useState('')
  const PAGE = 30

  useEffect(() => {
    let alive = true
    setPoints(null)
    setChartErr('')
    const to = Date.now()
    const from = to - range.hours * 3600_000
    linkTraffic(link.id, range.bucket, from, to)
      .then((d) => alive && setPoints(d))
      .catch((e) => alive && setChartErr((e as Error).message))
    return () => {
      alive = false
    }
  }, [link.id, range])

  useEffect(() => {
    let alive = true
    setConnLoading(true)
    setConnErr('')
    linkConnections(link.id, { q, offset, limit: PAGE })
      .then((p) => {
        if (!alive) return
        setConns(p.rows)
        setConnTotal(p.total)
      })
      .catch((e) => alive && setConnErr((e as Error).message))
      .finally(() => alive && setConnLoading(false))
    return () => {
      alive = false
    }
  }, [link.id, q, offset])

  const totalPages = Math.max(1, Math.ceil(connTotal / PAGE))
  const curPage = Math.floor(offset / PAGE) + 1

  const pageButtons = useMemo(() => {
    const lo = Math.max(1, curPage - 2)
    const hi = Math.min(totalPages, curPage + 2)
    const out: number[] = []
    for (let p = lo; p <= hi; p++) out.push(p)
    return out
  }, [curPage, totalPages])

  const columns: TableColumnsType<ConnectionRecord> = [
    {
      title: '时间',
      key: 'ts',
      render: (_, c) => <span className="mono">{formatTs(c.ts)}</span>,
    },
    {
      title: '目标',
      key: 'host',
      render: (_, c) => <span className="mono">{c.host || '—'}</span>,
    },
    {
      title: '端口',
      key: 'port',
      align: 'right',
      render: (_, c) => <span className="mono">{c.port ?? '—'}</span>,
    },
    {
      title: '用户',
      key: 'email',
      render: (_, c) => <span className="mono">{c.email}</span>,
    },
  ]

  return (
    <Modal title={`链接详情 · ${link.note || link.id}`} onClose={onClose} width={760}>
      <Space direction="vertical" size={22} style={{ width: '100%' }}>
        {/* A. 流量趋势 */}
        <div>
          <Space
            size={10}
            wrap
            align="center"
            style={{ width: '100%', justifyContent: 'space-between', marginBottom: 10 }}
          >
            <Typography.Text strong>流量趋势</Typography.Text>
            <Space size={8} wrap>
              {RANGES.map((r) => (
                <Button
                  key={r.label}
                  autoInsertSpace={false}
                  size="small"
                  type={range.label === r.label ? 'primary' : 'default'}
                  onClick={() => {
                    setRange(r)
                    setPoints(null)
                  }}
                >
                  {r.label}
                </Button>
              ))}
            </Space>
          </Space>
          {chartErr ? (
            <Alert type="error" showIcon message={chartErr} />
          ) : points === null ? (
            <Typography.Text type="secondary">加载中…</Typography.Text>
          ) : points.length === 0 ? (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该时间段暂无流量数据" />
          ) : (
            <TrafficChart points={points} />
          )}
        </div>

        {/* B. 连接记录 */}
        <div>
          <Space
            size={10}
            wrap
            align="center"
            style={{ width: '100%', justifyContent: 'space-between', marginBottom: 10 }}
          >
            <Typography.Text strong>连接记录</Typography.Text>
            <Space size={6} wrap className="touch-gap">
              <Input
                size="small"
                style={{ width: 200 }}
                placeholder="按域名搜索（如 google）"
                value={searchInput}
                onChange={(e) => setSearchInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    setQ(searchInput.trim())
                    setOffset(0)
                  }
                }}
              />
              <Button
                autoInsertSpace={false}
                size="small"
                onClick={() => {
                  setQ(searchInput.trim())
                  setOffset(0)
                }}
              >
                搜索
              </Button>
              {q && (
                <Button
                  autoInsertSpace={false}
                  size="small"
                  type="text"
                  onClick={() => {
                    setQ('')
                    setSearchInput('')
                    setOffset(0)
                  }}
                >
                  清除
                </Button>
              )}
            </Space>
          </Space>
          {connErr ? (
            <Alert type="error" showIcon message={connErr} />
          ) : connLoading ? (
            <Typography.Text type="secondary">加载中…</Typography.Text>
          ) : conns.length === 0 ? (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description="暂无连接记录（access log 采集启用后可见）"
            />
          ) : (
            <>
              <Table<ConnectionRecord>
                rowKey="id"
                size="small"
                columns={columns}
                dataSource={conns}
                pagination={false}
                scroll={{ x: 'max-content' }}
                sticky
              />
              <Space size={6} wrap className="touch-gap" style={{ marginTop: 12 }}>
                <Button
                  autoInsertSpace={false}
                  size="small"
                  disabled={offset <= 0}
                  onClick={() => setOffset(Math.max(0, offset - PAGE))}
                >
                  ← 上一页
                </Button>
                <Typography.Text type="secondary">
                  {connTotal} 条 · 第 {curPage}/{totalPages} 页
                </Typography.Text>
                {pageButtons.map((p) => (
                  <Button
                    key={p}
                    autoInsertSpace={false}
                    size="small"
                    type={p === curPage ? 'primary' : 'default'}
                    onClick={() => setOffset((p - 1) * PAGE)}
                  >
                    {p}
                  </Button>
                ))}
                <Button
                  autoInsertSpace={false}
                  size="small"
                  disabled={curPage >= totalPages}
                  onClick={() => setOffset(offset + PAGE)}
                >
                  下一页 →
                </Button>
              </Space>
            </>
          )}
        </div>
      </Space>
    </Modal>
  )
}

function formatTs(ts: number): string {
  const d = new Date(ts)
  const p = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
