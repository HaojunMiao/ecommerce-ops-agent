import { Alert, Card, Form, Input, Button, Typography, message } from 'antd'
import { UserOutlined, LockOutlined } from '@ant-design/icons'
import { useState } from 'react'
import { Redirect, useLocation, useSearch } from 'wouter'
import { getLoginErrorMessage, login, type LoginRequest } from '@/api/auth'
import { useAuthStore } from '@/store/authStore'
import './LoginPage.css'

export function LoginPage() {
  const [, navigate] = useLocation()
  const search = useSearch()
  const { token, setAuth } = useAuthStore()
  const [loading, setLoading] = useState(false)
  const [loginError, setLoginError] = useState('')

  // 已登录直接进首页。
  if (token) return <Redirect to="/agents" />

  const requestedPath = new URLSearchParams(search).get('from')
  const isInternalPath = requestedPath?.startsWith('/') && !requestedPath.startsWith('//') && !requestedPath.startsWith('/\\')
  const from = isInternalPath && requestedPath ? requestedPath : '/agents'

  async function onFinish(values: LoginRequest) {
    setLoginError('')
    setLoading(true)
    try {
      const resp = await login(values)
      setAuth(resp.token, resp.user)
      message.success('登录成功')
      navigate(from, { replace: true })
    } catch (error) {
      setLoginError(getLoginErrorMessage(error))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="login-page">
      <Card className="login-card">
        <div className="login-brand">
          <span className="login-brand-mark" aria-hidden="true">
            EO
          </span>
          <Typography.Title level={3} style={{ margin: 0 }}>
            电商运营 Agent 管理台
          </Typography.Title>
          <Typography.Text type="secondary">企业级 AI Agent 平台</Typography.Text>
        </div>
        {loginError && <Alert type="error" showIcon message={loginError} style={{ marginBottom: 16 }} />}
        <Form layout="vertical" onFinish={onFinish} initialValues={{ email: '', password: '' }}>
          <Form.Item
            name="email"
            rules={[{ required: true, message: '请输入邮箱' }, { type: 'email', message: '邮箱格式不对' }]}
          >
            <Input prefix={<UserOutlined />} placeholder="邮箱" autoComplete="username" />
          </Form.Item>
          <Form.Item name="password" rules={[{ required: true, message: '请输入密码' }]}>
            <Input.Password prefix={<LockOutlined />} placeholder="密码" autoComplete="current-password" />
          </Form.Item>
          <Form.Item style={{ marginBottom: 0 }}>
            <Button type="primary" htmlType="submit" block loading={loading}>
              登录
            </Button>
          </Form.Item>
        </Form>
      </Card>
    </div>
  )
}
