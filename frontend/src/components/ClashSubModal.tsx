import { useCallback, useEffect, useState } from 'react'
import { Alert, Button, Space, Typography } from 'antd'
import QRCode from 'qrcode'
import Modal from './Modal'
import { createClashSubscription } from '../api'
import { formatCountdown } from '../lib/format'
import type { ClashSubResult } from '../types'

// 生成 Clash 订阅（多选节点）：打开即调 POST，展示二维码 + 订阅链接 + 一键导入链接 + 倒计时。
// 订阅是临时凭证：过期后提示重新生成（同一批 linkIds 再 POST 一次）。
// 勾选态由父组件 Dashboard 持有，本组件只消费 linkIds，关闭不改变勾选。
// 本轮改 antd：Modal/Space/Typography/Button/Alert。二维码仍由 qrcode 生成 dataURL（保留原渲染能力，
// 只换外层容器与说明文字排版）；订阅地址、参数说明、复制内容与提示一律不变。

/** ttl 秒数 → 中文提示片段：600 → 「10 分钟」，90 → 「2 分钟」，45 → 「45 秒」 */
function ttlLabel(seconds: number): string {
  if (seconds >= 60) {
    const m = Math.round(seconds / 60)
    return `${m} 分钟`
  }
  return `${seconds} 秒`
}

export function ClashSubModal({ linkIds, onClose }: { linkIds: string[]; onClose: () => void }) {
  const [result, setResult] = useState<ClashSubResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [qr, setQr] = useState('')
  const [copied, setCopied] = useState<'' | 'url' | 'import'>('')
  const [now, setNow] = useState(() => Date.now())

  const generate = useCallback(async () => {
    setLoading(true)
    setError('')
    setResult(null)
    setQr('')
    try {
      const r = await createClashSubscription(linkIds)
      setResult(r)
      setNow(Date.now())
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [linkIds])

  useEffect(() => {
    void generate()
  }, [generate])

  useEffect(() => {
    if (!result) return
    let alive = true
    QRCode.toDataURL(result.url, { margin: 1, width: 260 })
      .then((u) => alive && setQr(u))
      .catch(() => alive && setQr(''))
    return () => {
      alive = false
    }
  }, [result])

  useEffect(() => {
    if (!result) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [result])

  async function copy(text: string, kind: 'url' | 'import') {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(kind)
      setTimeout(() => setCopied(''), 1500)
    } catch {
      window.prompt(kind === 'url' ? '复制订阅链接' : '复制导入链接', text)
    }
  }

  const remaining = result ? result.expiresAt - now : 0
  const expired = result !== null && remaining <= 0

  return (
    <Modal title="Clash 订阅" onClose={onClose} width={360}>
      {loading ? (
        <Typography.Text type="secondary">正在生成…</Typography.Text>
      ) : error ? (
        <Alert type="error" showIcon message={error} />
      ) : result ? (
        <Space direction="vertical" size={14} align="center" style={{ width: '100%' }}>
          {expired ? (
            <>
              <Typography.Text type="secondary">已过期，请重新生成</Typography.Text>
              <Button
                autoInsertSpace={false}
                type="primary"
                block
                onClick={() => void generate()}
              >
                重新生成
              </Button>
            </>
          ) : (
            <>
              {qr ? (
                <img src={qr} alt="订阅二维码" className="qr" />
              ) : (
                <div className="qr qr-empty">…</div>
              )}
              <Typography.Text type="secondary">
                剩余 {formatCountdown(remaining)}（共 {result.count} 个节点）
              </Typography.Text>
              <div className="vless-uri">{result.url}</div>
              <Button
                autoInsertSpace={false}
                block
                onClick={() => void copy(result.url, 'url')}
              >
                {copied === 'url' ? '已复制订阅链接' : '复制订阅链接'}
              </Button>
              <Button
                autoInsertSpace={false}
                block
                onClick={() => void copy(result.importUrl, 'import')}
              >
                {copied === 'import' ? '已复制导入链接' : '复制 clash:// 导入链接'}
              </Button>
            </>
          )}
          {result.skipped.length > 0 && (
            <Typography.Text type="secondary">
              已忽略 {result.skipped.length} 个不存在的链接
            </Typography.Text>
          )}
          <Typography.Text type="secondary">
            订阅默认国内直连、其余走代理；如需全部走代理，在订阅链接后加 ?mode=global。
          </Typography.Text>
          <Typography.Text type="secondary">
            订阅本身 {ttlLabel(result.ttlSeconds)}内有效；节点是否可用取决于各链接自身有效期。
          </Typography.Text>
        </Space>
      ) : null}
    </Modal>
  )
}
