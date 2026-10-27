import { App as AntApp, Button, ConfigProvider, Flex, Layout, Menu } from 'antd'
import { BarChartOutlined, CloudUploadOutlined, DashboardOutlined, KeyOutlined, MobileOutlined } from '@ant-design/icons'
import zhCN from 'antd/locale/zh_CN'
import { BrowserRouter, Navigate, Outlet, Route, Routes, useLocation, useNavigate } from 'react-router'
import { apiGet, tokenStore } from './api/client'
import type { Me, Meta } from './api/types'
import { useApi } from './api/useApi'
import AppKeys from './pages/AppKeys'
import InstallDetail from './pages/InstallDetail'
import Installs from './pages/Installs'
import Login from './pages/Login'
import Overview from './pages/Overview'
import Uploads from './pages/Uploads'
import Usage from './pages/Usage'

const MENU = [
  { key: '/', icon: <DashboardOutlined />, label: '概览' },
  { key: '/usage', icon: <BarChartOutlined />, label: '用量统计' },
  { key: '/installs', icon: <MobileOutlined />, label: '安装' },
  { key: '/uploads', icon: <CloudUploadOutlined />, label: '上传记录' },
  { key: '/app-keys', icon: <KeyOutlined />, label: 'App Key' },
]

function Shell() {
  const location = useLocation()
  const navigate = useNavigate()
  const me = useApi(() => apiGet<Me>('/me'), [])
  const meta = useApi(() => apiGet<Meta>('/meta'), [])
  const selected = '/' + (location.pathname.split('/')[1] ?? '')

  const logout = () => {
    tokenStore.clear()
    navigate('/login', { replace: true })
  }

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Layout.Sider theme="light" width={200} breakpoint="lg" collapsedWidth={0}>
        <div style={{ padding: '20px 24px', fontWeight: 600 }}>阿福计量后台</div>
        <Menu mode="inline" selectedKeys={[selected]} items={MENU} onClick={(e) => navigate(e.key)} />
      </Layout.Sider>
      <Layout>
        <Layout.Header style={{ background: '#fff', paddingInline: 24 }}>
          <Flex justify="flex-end" align="center" gap={16} style={{ height: '100%' }}>
            {meta.data && <span style={{ color: '#999' }}>版本 {meta.data.version}</span>}
            <span>{me.data?.name}</span>
            <Button size="small" onClick={logout}>
              退出
            </Button>
          </Flex>
        </Layout.Header>
        <Layout.Content style={{ padding: 24 }}>
          <Outlet />
        </Layout.Content>
      </Layout>
    </Layout>
  )
}

function RequireAuth() {
  return tokenStore.get() ? <Shell /> : <Navigate to="/login" replace />
}

export default function App() {
  return (
    <ConfigProvider locale={zhCN}>
      <AntApp>
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route element={<RequireAuth />}>
              <Route index element={<Overview />} />
              <Route path="usage" element={<Usage />} />
              <Route path="installs" element={<Installs />} />
              <Route path="installs/:installId" element={<InstallDetail />} />
              <Route path="uploads" element={<Uploads />} />
              <Route path="app-keys" element={<AppKeys />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Route>
          </Routes>
        </BrowserRouter>
      </AntApp>
    </ConfigProvider>
  )
}
