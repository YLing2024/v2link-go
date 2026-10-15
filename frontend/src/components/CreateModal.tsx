import { useState } from 'react'
import { Alert, Button, Form, Input, Space, Typography } from 'antd'
import { createLink } from '../api'
import Modal from './Modal'
import { defaultExpiry, toDateTimeLocal, toEpochMs } from '../lib/datetime'
import { humanDuration } from '../lib/format'

// 「生成链接」弹窗：**过期时刻（分钟精度）是唯一输入**；小时档只是「一键把时刻设到此刻 + N」的便捷键，
// 选定后仍可在日期框里手改。剩余时长以提示形式在旁边显示（用户 2026-09-14 反馈）。
// 需求 §6：时长快捷 1h/6h/24h/3d/7d + 自定义 hours；用户 2026-09-14 追加「永久有效」档位。
// 本轮改 antd：Modal/Form/Input/Button/Space/Alert/Typography。字段、默认值、有效期选项、
// 校验规则与错误提示、按钮顺序与提交/刷新行为一律不变。

export const QUICK_EXPIRY: { label: string; hours: number }[] = [
  { label: '1 小时', hours: 1 },
  { label: '6 小时', hours: 6 },
  { label: '24 小时', hours: 24 },
  { label: '3 天', hours: 72 },
  { label: '7 天', hours: 168 },
]

/** 绝对时刻上限（与后端 MAX_EXPIRE_ABS_DAYS 一致） */
const MAX_EXPIRE_DAYS = 365

export function CreateModal({
  onClose,
  onCreated,
}: {
  onClose: () => void
  onCreated: () => void
}) {
  const [expiryInput, setExpiryInput] = useState(() => toDateTimeLocal(defaultExpiry()))
  // 高亮的便捷档（手改时刻后清空）——唯一状态，避免和输入框内容互相推导
  const [quickHours, setQuickHours] = useState<number | null>(24)
  const [permanent, setPermanent] = useState(false)
  const [note, setNote] = useState('')
  const [alias, setAlias] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [err, setErr] = useState('')

  const expiryMs = toEpochMs(expiryInput)

  const hint = (() => {
    if (permanent) return '永久链接不会自动过期，需手动吊销。'
    if (expiryMs === null) return ''
    const diff = expiryMs - Date.now()
    if (diff <= 0) return '该时刻已过去，请选择未来时间'
    if (diff > MAX_EXPIRE_DAYS * 24 * 3600 * 1000) return `不能超过 ${MAX_EXPIRE_DAYS} 天`
    return `距现在约 ${humanDuration(diff)}`
  })()

  function pickQuick(hours: number) {
    setPermanent(false)
    setQuickHours(hours)
    setExpiryInput(toDateTimeLocal(Date.now() + hours * 3600 * 1000))
  }

  async function submit() {
    setErr('')
    let expiresAt: number | undefined
    if (!permanent) {
      const ms = toEpochMs(expiryInput)
      if (ms === null) {
        setErr('请选择过期时间')
        return
      }
      if (ms <= Date.now()) {
        setErr('过期时间须晚于当前时间')
        return
      }
      if (ms - Date.now() > MAX_EXPIRE_DAYS * 24 * 3600 * 1000) {
        setErr(`过期时间不能超过 ${MAX_EXPIRE_DAYS} 天`)
        return
      }
      expiresAt = ms
    }
    setSubmitting(true)
    try {
      await createLink(
        permanent
          ? { note: note || undefined, alias: alias || undefined, permanent: true }
          : { note: note || undefined, alias: alias || undefined, expiresAt },
      )
      onCreated()
      onClose()
    } catch (e) {
      setErr((e as Error).message)
      setSubmitting(false)
    }
  }

  return (
    <Modal title="生成链接" onClose={onClose}>
      <Form layout="vertical" requiredMark={false}>
        <Form.Item label="过期时间" style={{ marginBottom: 16 }}>
          <Space direction="vertical" size={8} style={{ width: '100%' }}>
            <Space size={8} wrap>
              {QUICK_EXPIRY.map((q) => (
                <Button
                  key={q.hours}
                  autoInsertSpace={false}
                  type={!permanent && quickHours === q.hours ? 'primary' : 'default'}
                  onClick={() => pickQuick(q.hours)}
                >
                  {q.label}
                </Button>
              ))}
              <Button
                autoInsertSpace={false}
                type={permanent ? 'primary' : 'default'}
                onClick={() => setPermanent(true)}
              >
                永久
              </Button>
            </Space>
            {!permanent && (
              <Input
                type="datetime-local"
                value={expiryInput}
                onChange={(e) => {
                  setExpiryInput(e.target.value)
                  setQuickHours(null)
                }}
              />
            )}
            {hint && <Typography.Text type="secondary">{hint}</Typography.Text>}
          </Space>
        </Form.Item>

        <Form.Item label="别名" style={{ marginBottom: 16 }}>
          <Input
            maxLength={100}
            placeholder="客户端里显示的节点名（留空用备注）"
            value={alias}
            onChange={(e) => setAlias(e.target.value)}
          />
        </Form.Item>

        <Form.Item label="备注" style={{ marginBottom: 16 }}>
          <Input
            maxLength={200}
            placeholder="给谁用 / 用途（可选）"
            value={note}
            onChange={(e) => setNote(e.target.value)}
          />
        </Form.Item>

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
            {submitting ? '生成中…' : '生成'}
          </Button>
        </Space>
      </Form>
    </Modal>
  )
}
