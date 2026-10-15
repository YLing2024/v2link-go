import { useEffect, useState } from 'react'
import { Space, Tag, Tooltip, Typography } from 'antd'
import { regionProbes } from '../api'
import { formatRelative } from '../lib/format'
import { probeLabel, toneLabel, toneOf, type RegionTone } from '../lib/regionProbe'
import type { RegionProbeSnapshot } from '../types'

// 底部全球连通性监控条（地区连通性监控）：
//   · 数据来自后端 GET /api/regions/probes（scheduler 每 5min 一轮 + 启动即首轮）
//   · 横向一排地区徽标：国旗 + 名称 + 延迟/失败；色 = 延迟/失败（阈值见 lib/regionProbe.ts）
//   · 展示挂载时拉取一次（页面停留期间不主动轮询——数据源 5min 才更新，刷新页面即见最新）
//   · 附带「N 分钟前更新」时间戳
// 本轮仅按 antd（Tag / Tooltip / Space / Typography）重排视觉，探测逻辑、地址与阈值未动。

const TICK_MS = 60_000 // 页面停留时每分钟刷新一次「N 分钟前」显示

// 绿=正常 <300ms；金=偏慢 300~800ms；红=失败/不可达（阈值由 toneOf 判定）。
const TONE_COLOR: Record<RegionTone, string> = {
  green: 'success',
  yellow: 'warning',
  red: 'error',
}

export function RegionProbeBar() {
  const [snap, setSnap] = useState<RegionProbeSnapshot | null>(null)
  const [err, setErr] = useState('')
  const [now, setNow] = useState(Date.now())

  useEffect(() => {
    let alive = true
    setErr('')
    regionProbes()
      .then((d) => alive && setSnap(d))
      .catch((e) => alive && setErr((e as Error).message))
    const t = setInterval(() => alive && setNow(Date.now()), TICK_MS)
    return () => {
      alive = false
      clearInterval(t)
    }
  }, [])

  const probes = snap?.probes ?? []
  const updatedAt = snap?.updatedAt ?? 0

  return (
    <footer className="regionbar">
      <Typography.Text type="secondary" className="regionbar-title">
        地区连通性
      </Typography.Text>
      {err ? (
        <Typography.Text type="danger">{err}</Typography.Text>
      ) : probes.length === 0 ? (
        <Typography.Text type="secondary">加载中…</Typography.Text>
      ) : (
        <Space size={8}>
          {probes.map((p) => {
            const tone = toneOf(p)
            return (
              <Tooltip
                key={p.key}
                title={`${p.flag} ${p.name} · ${toneLabel(tone)}${p.rttMs !== null ? ` · ${p.rttMs}ms` : ''}${p.error ? ` · ${p.error}` : ''}`}
              >
                <Tag color={TONE_COLOR[tone]}>
                  {p.flag} {p.name} <span className="mono">{probeLabel(p)}</span>
                </Tag>
              </Tooltip>
            )
          })}
        </Space>
      )}
      <Typography.Text type="secondary" className="regionbar-ts">
        {updatedAt ? `${formatRelative(updatedAt, now)}更新` : '尚未探测'}
      </Typography.Text>
    </footer>
  )
}
