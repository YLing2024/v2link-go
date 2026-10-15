import { useEffect, useState } from 'react'
import { Spin, Typography } from 'antd'
import Dashboard from './components/Dashboard'
import Login from './components/Login'
import { getAuthMode, me, setUnauthorizedHandler } from './api'

// 认证入口层：
//   sso     —— 直接渲染面板；未登录时由统一 client 整页跳前置认证登录页（保持现状行为）。
//   builtin —— 先探当前会话：已登录进面板，未登录渲染本地登录页。
// 面板 / 弹窗交互与样式不在本层改动。
type Phase = 'loading' | 'login' | 'app'

export default function App() {
  const [phase, setPhase] = useState<Phase>('loading')

  useEffect(() => {
    let alive = true
    void (async () => {
      const mode = await getAuthMode()
      if (!alive) return
      if (mode === 'sso') {
        setPhase('app')
        return
      }
      const user = await me()
      if (alive) setPhase(user ? 'app' : 'login')
    })()
    return () => {
      alive = false
    }
  }, [])

  // builtin 下全局 401 → 切回本地登录页
  useEffect(() => {
    setUnauthorizedHandler(() => setPhase('login'))
    return () => setUnauthorizedHandler(null)
  }, [])

  if (phase === 'loading') {
    return (
      <div className="app-loading">
        <Typography.Title level={3} className="app-loading-logo">
          v2link
        </Typography.Title>
        <Spin />
        <Typography.Text type="secondary">正在载入…</Typography.Text>
      </div>
    )
  }

  if (phase === 'login') {
    return <Login onSuccess={() => setPhase('app')} />
  }

  return <Dashboard />
}
