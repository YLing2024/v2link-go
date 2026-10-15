import { useEffect, useState } from 'react'
import { Alert, Button, Form, Input, Typography } from 'antd'
import { ApiError, login } from '../api'

// 本地登录页（AUTH_MODE=builtin）：账号 + 口令 → 本地会话。
// 改用 antd Form/Input/Button/Alert；认证语义、错误文案、会话 cookie 行为与旧实现一致。

interface LoginForm {
  username: string
  password: string
}

export default function Login({ onSuccess }: { onSuccess: (name: string) => void }) {
  const [form] = Form.useForm<LoginForm>()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [cooldown, setCooldown] = useState(0)
  const username = Form.useWatch('username', form)
  const password = Form.useWatch('password', form)

  useEffect(() => {
    if (cooldown <= 0) return
    const timer = window.setInterval(() => setCooldown((s) => (s > 0 ? s - 1 : 0)), 1000)
    return () => window.clearInterval(timer)
  }, [cooldown])

  async function submit(values: LoginForm) {
    if (loading || cooldown > 0) return
    setError('')
    setLoading(true)
    try {
      const user = await login(values.username.trim(), values.password)
      onSuccess(user.name)
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        setCooldown(err.retryAfter && err.retryAfter > 0 ? err.retryAfter : 60)
        setError('尝试过于频繁')
      } else {
        setError((err as Error).message || '登录失败')
      }
    } finally {
      setLoading(false)
    }
  }

  const blocked = loading || cooldown > 0

  return (
    <div className="login-screen">
      <div className="login-card">
        <Typography.Title level={3} className="login-logo">
          v2link
        </Typography.Title>
        <Typography.Text type="secondary">临时 VLESS 链接</Typography.Text>
        <Form
          form={form}
          layout="vertical"
          requiredMark={false}
          className="login-form"
          onFinish={submit}
        >
          {error && (
            <Alert
              className="login-error"
              type="error"
              showIcon
              message={cooldown > 0 ? `尝试过于频繁，请 ${cooldown} 秒后再试` : error}
            />
          )}
          <Form.Item label="账号" name="username">
            <Input autoComplete="username" autoFocus disabled={blocked} />
          </Form.Item>
          <Form.Item label="口令" name="password">
            <Input.Password autoComplete="current-password" disabled={blocked} />
          </Form.Item>
          <Button
            autoInsertSpace={false}
            type="primary"
            htmlType="submit"
            block
            loading={loading}
            disabled={blocked || !username?.trim() || !password}
          >
            {loading ? '登录中…' : '登录'}
          </Button>
        </Form>
      </div>
    </div>
  )
}
