import { useEffect, useState } from 'react'
import { Alert, Button, Space, Table, Tag, Typography } from 'antd'
import type { TableColumnsType } from 'antd'
import { auditLog } from '../api'
import type { AuditRecord } from '../types'
import Modal from './Modal'

// 操作审计（审计）：后台 create/revoke/extend 留痕列表（分页）。
// 入口放 Dashboard 顶部栏「审计」。
// 本轮改 antd：Modal/Table/Tag/Typography/Button/Alert/Space。字段与顺序（时间/操作人/动作/链接/详情）不变；
// 动作标签用 antd Tag 预设色（语义与原 audit-* 一致）；分页沿用原文案与按钮顺序。
// 表格加 scroll.x：窄屏可横向滚动，避免右侧列（详情）被裁切。

const ACTION_LABEL: Record<AuditRecord['action'], string> = {
  create: '生成',
  revoke: '吊销',
  extend: '延长',
  link_delete: '删除',
  clash_sub_create: 'Clash 订阅',
}

// 语义色：生成=主色(processing)、延长=成功(green)、吊销/删除=错误(red)、Clash 订阅=中性(默认)。
const ACTION_COLOR: Record<AuditRecord['action'], string | undefined> = {
  create: 'processing',
  revoke: 'error',
  extend: 'success',
  link_delete: 'error',
  clash_sub_create: undefined,
}

export function AuditModal({ onClose }: { onClose: () => void }) {
  const [rows, setRows] = useState<AuditRecord[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')
  const PAGE = 30

  useEffect(() => {
    let alive = true
    setLoading(true)
    setErr('')
    auditLog({ limit: PAGE, offset })
      .then((p) => {
        if (!alive) return
        setRows(p.rows)
        setTotal(p.total)
      })
      .catch((e) => alive && setErr((e as Error).message))
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
  }, [offset])

  const totalPages = Math.max(1, Math.ceil(total / PAGE))
  const curPage = Math.floor(offset / PAGE) + 1

  const columns: TableColumnsType<AuditRecord> = [
    {
      title: '时间',
      key: 'ts',
      render: (_, r) => <span className="mono">{formatTs(r.ts)}</span>,
    },
    {
      title: '操作人',
      key: 'actor',
      dataIndex: 'actor',
    },
    {
      title: '动作',
      key: 'action',
      render: (_, r) => <Tag color={ACTION_COLOR[r.action]}>{ACTION_LABEL[r.action]}</Tag>,
    },
    {
      title: '链接',
      key: 'link',
      render: (_, r) => <span className="mono">{r.link_id || '—'}</span>,
    },
    {
      title: '详情',
      key: 'detail',
      width: 300,
      ellipsis: true,
      render: (_, r) => (
        <Typography.Text type="secondary" className="mono">
          {formatDetail(r)}
        </Typography.Text>
      ),
    },
  ]

  return (
    <Modal title="操作审计" onClose={onClose} width={760}>
      {err ? (
        <Alert type="error" showIcon message={err} />
      ) : loading ? (
        <Typography.Text type="secondary">加载中…</Typography.Text>
      ) : rows.length === 0 ? (
        <Typography.Text type="secondary">暂无审计记录</Typography.Text>
      ) : (
        <>
          <Table<AuditRecord>
            rowKey="id"
            size="small"
            columns={columns}
            dataSource={rows}
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
              {total} 条 · 第 {curPage}/{totalPages} 页
            </Typography.Text>
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
    </Modal>
  )
}

function formatDetail(r: AuditRecord): string {
  if (!r.detail) return ''
  const d = r.detail
  const parts: string[] = []
  if (r.action === 'clash_sub_create') {
    if (d.count !== undefined) parts.push(`${d.count} 个节点`)
    if (d.token_prefix) parts.push(`token ${String(d.token_prefix)}…`)
    return parts.join(' · ')
  }
  if (d.permanent) parts.push(r.action === 'extend' ? '转为永久' : '永久')
  if (d.from === 'permanent') parts.push('由永久转限时')
  if (r.action === 'link_delete') {
    // 后端 detail 带 summary text；缺失时回落结构化字段（别名/备注）
    if (d.text !== undefined) return String(d.text)
    if (d.alias) parts.push(`别名 ${String(d.alias)}`)
  }
  if (d.hours !== undefined) parts.push(`${d.hours}h`)
  if (d.note) parts.push(String(d.note))
  if (d.expires_at !== undefined) parts.push(`到期 ${formatTs(Number(d.expires_at))}`)
  return parts.join(' · ')
}

function formatTs(ts: number): string {
  const d = new Date(ts)
  const p = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
