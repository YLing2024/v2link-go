import { useState } from 'react'
import { Alert, Button, Form, Input, Space, Typography } from 'antd'
import Modal from './Modal'
import { extendLink } from '../api'
import { toDateTimeLocal, toEpochMs } from '../lib/datetime'
import { humanDuration } from '../lib/format'
import type { Link } from '../types'

// 「编辑过期时间」弹窗（用户 2026-09-14 反馈：这不是「延长/延时」，而是直接改过期时刻）。
//   · 主输入 = 过期时刻（datetime-local，分钟精度），预填当前到期时刻
//   · 限时链接可「转为永久」；永久链接无到期时刻，填时刻即转回限时
//   · 提前（缩短有效期）提交前二次确认；剩余时长以提示形式显示在旁边
// 本轮改 antd：Modal/Form/Input/Button/Space/Alert/Typography。字段、校验规则与错误提示、
// 二次确认文案与按钮顺序、提交/刷新行为一律不变。

type Mode = 'finite' | 'permanent'

export function ActModal({
  link,
  onClose,
  onDone,
}: {
  link: Link
  onClose: () => void
  onDone: (updated: Link) => void
}) {
  const [mode, setMode] = useState<Mode>('finite')
  const [absInput, setAbsInput] = useState<string>(
    link.permanent ? '' : toDateTimeLocal(link.expiresAt),
  )
  const [submitting, setSubmitting] = useState(false)
  const [err, setErr] = useState('')

  const hint = (() => {
    if (mode === 'permanent') {
      return link.permanent
        ? '当前为永久有效，不会自动过期。'
        : '转为永久后不再自动过期，需手动吊销；之后可随时再指定过期时间。'
    }
    const ms = toEpochMs(absInput)
    if (ms === null) {
      return link.permanent ? '当前为永久有效：选择过期时间即转为限时。' : ''
    }
    const diff = ms - Date.now()
    if (diff <= 0) return '该时刻已过去，请选择未来时间'
    const tip = `距现在约 ${humanDuration(diff)}`
    return ms < link.expiresAt ? `${tip}（比当前到期时间早，将缩短有效期）` : tip
  })()

  async function submit() {
    setErr('')
    if (mode === 'permanent') {
      const ok = window.confirm('转为永久有效后不再自动过期，只能手动吊销。确认？')
      if (!ok) return
      await doSubmit({ permanent: true })
      return
    }
    const ms = toEpochMs(absInput)
    if (ms === null) {
      setErr('请选择过期时间')
      return
    }
    if (ms <= Date.now()) {
      setErr('过期时间须晚于当前时间')
      return
    }
    // 缩短有效期 → 二次确认（编辑语义下这是有意义的操作，但容易误点）
    if (!link.permanent && ms < link.expiresAt) {
      const ok = window.confirm('新的过期时间早于当前，将缩短有效期。确认？')
      if (!ok) return
    }
    await doSubmit({ expiresAt: ms })
  }

  async function doSubmit(body: { expiresAt?: number; permanent?: boolean }) {
    setSubmitting(true)
    try {
      const updated = await extendLink(link.id, body)
      onDone(updated)
      onClose()
    } catch (e) {
      setErr((e as Error).message)
      setSubmitting(false)
    }
  }

  return (
    <Modal title="编辑过期时间" onClose={onClose}>
      <Form layout="vertical" requiredMark={false}>
        {!link.permanent && (
          <Form.Item label="方式" style={{ marginBottom: 16 }}>
            <Space size={8} wrap>
              <Button
                autoInsertSpace={false}
                type={mode === 'finite' ? 'primary' : 'default'}
                onClick={() => {
                  setMode('finite')
                  setAbsInput(toDateTimeLocal(link.expiresAt))
                }}
              >
                指定时刻
              </Button>
              <Button
                autoInsertSpace={false}
                type={mode === 'permanent' ? 'primary' : 'default'}
                onClick={() => setMode('permanent')}
              >
                转为永久
              </Button>
            </Space>
          </Form.Item>
        )}

        {mode === 'finite' ? (
          <Form.Item label="过期时间（本地时间）" style={{ marginBottom: 16 }}>
            <Input
              type="datetime-local"
              value={absInput}
              autoFocus
              onChange={(e) => setAbsInput(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && void submit()}
            />
            {hint && <Typography.Text type="secondary">{hint}</Typography.Text>}
          </Form.Item>
        ) : (
          <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 16 }}>
            转为永久后不再自动过期，需手动吊销；之后可随时再指定过期时间。
          </Typography.Text>
        )}

        {err && <Alert type="error" showIcon message={err} style={{ marginBottom: 12 }} />}

        <Space style={{ width: '100%', justifyContent: 'flex-end' }}>
          <Button autoInsertSpace={false} onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button
            autoInsertSpace={false}
            type="primary"
            onClick={() => void submit()}
            disabled={submitting}
          >
            {submitting ? '提交中…' : '确定'}
          </Button>
        </Space>
      </Form>
    </Modal>
  )
}
