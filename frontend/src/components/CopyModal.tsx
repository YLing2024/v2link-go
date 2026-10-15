import { useEffect, useState } from 'react'
import { Button, Space, Typography } from 'antd'
import QRCode from 'qrcode'
import Modal from './Modal'
import { displayAlias, vlessLink } from '../lib/vless'
import type { Link } from '../types'

// 复制链接 & 二维码弹窗（点「复制」也顺带展示二维码便于扫码）
// 别名（客户端节点名）写进 vless:// 的 #fragment：alias → note → id 回退，导入即有名。
// 本轮改 antd：Modal/Space/Typography/Button。二维码仍由 qrcode 生成 dataURL 渲染（保留原渲染能力，
// 只换外层容器与说明文字排版）；vless 生成语义、复制内容与提示、剪贴板回退行为一律不变。
export function CopyModal({ link, onClose }: { link: Link; onClose: () => void }) {
  const name = displayAlias(link)
  const uri = vlessLink(link.uuid, undefined, undefined, name)
  const [qr, setQr] = useState('')
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    QRCode.toDataURL(uri, { margin: 1, width: 260 })
      .then((url) => setQr(url))
      .catch(() => setQr(''))
  }, [uri])

  async function copy() {
    try {
      await navigator.clipboard.writeText(uri)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // 剪贴板不可用（非 https 等）：回退提示手动复制
      window.prompt('复制链接', uri)
    }
  }

  return (
    <Modal title="链接详情" onClose={onClose} width={340}>
      <Space direction="vertical" size={14} align="center" style={{ width: '100%' }}>
        {qr ? <img src={qr} alt="二维码" className="qr" /> : <div className="qr qr-empty">…</div>}
        <div className="vless-uri">{uri}</div>
        <Typography.Text type="secondary">
          {link.alias
            ? `节点名：${name}`
            : `节点名：${name}（未设别名，回退${link.note ? '备注' : '链接 ID'}）`}
        </Typography.Text>
        <Button autoInsertSpace={false} type="primary" block onClick={copy}>
          {copied ? '已复制' : '复制链接'}
        </Button>
      </Space>
    </Modal>
  )
}
