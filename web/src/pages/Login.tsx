import { useState } from 'react'
import { Alert, Button, Card, Flex, Form, Input } from 'antd'
import { useNavigate } from 'react-router'
import { ApiError, apiGet, errorText, tokenStore } from '../api/client'
import type { Me } from '../api/types'

export default function Login() {
  const navigate = useNavigate()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string>()

  const onFinish = async ({ token }: { token: string }) => {
    const t = token.trim()
    setLoading(true)
    setError(undefined)
    try {
      await apiGet<Me>('/me', undefined, { token: t, redirectOn401: false }) // 先校验，通过才保存
      tokenStore.set(t)
      navigate('/', { replace: true })
    } catch (e) {
      setError(e instanceof ApiError && e.status === 401 ? 'Token 不正确' : errorText(e))
    } finally {
      setLoading(false)
    }
  }

  return (
    <Flex align="center" justify="center" style={{ minHeight: '100vh', background: '#f5f5f5' }}>
      <Card title="阿福计量 · 内部数据后台" style={{ width: 380 }}>
        <Form layout="vertical" onFinish={onFinish}>
          <Form.Item name="token" label="Token" rules={[{ required: true, message: '请输入 Token' }]}>
            <Input.Password autoFocus placeholder="向管理员申请" />
          </Form.Item>
          {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 16 }} />}
          <Button type="primary" htmlType="submit" block loading={loading}>
            登录
          </Button>
        </Form>
      </Card>
    </Flex>
  )
}
