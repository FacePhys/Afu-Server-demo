import { useState } from 'react'
import { Alert, App, Button, Flex, Form, Input, Modal, Popconfirm, Table, Tag, Typography, type TableColumnsType } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { apiGet, apiSend, errorText } from '../api/client'
import type { AppKey, AppKeyList } from '../api/types'
import { useApi } from '../api/useApi'
import { fmtTime } from '../utils/time'

interface CreateForm {
  remark?: string
  app_key?: string
}

// AppKeys App Key 管理：生成(或导入已发给客户的)、停用 / 启用。注册、上报只认已启用的 App Key。
export default function AppKeys() {
  const { message } = App.useApp()
  const { data, loading, error, reload } = useApi(() => apiGet<AppKeyList>('/app-keys'), [])
  const [creating, setCreating] = useState(false)
  const [saving, setSaving] = useState(false)
  const [form] = Form.useForm<CreateForm>()

  const create = async (v: CreateForm) => {
    setSaving(true)
    try {
      const k = await apiSend<AppKey>('POST', '/app-keys', { remark: v.remark?.trim() ?? '', app_key: v.app_key?.trim() ?? '' })
      message.success(`已创建 ${k.app_key}`)
      setCreating(false)
      form.resetFields()
      reload()
    } catch (e) {
      message.error(errorText(e))
    } finally {
      setSaving(false)
    }
  }

  const setStatus = async (k: AppKey, status: AppKey['status']) => {
    try {
      await apiSend<AppKey>('PATCH', `/app-keys/${k.id}`, { status })
      message.success(status === 'enabled' ? '已启用' : '已停用')
      reload()
    } catch (e) {
      message.error(errorText(e))
    }
  }

  const columns: TableColumnsType<AppKey> = [
    {
      title: 'App Key',
      dataIndex: 'app_key',
      render: (v: string) => (
        <Typography.Text code copyable={{ text: v }}>
          {v}
        </Typography.Text>
      ),
    },
    { title: '备注', dataIndex: 'remark' },
    {
      title: '状态',
      dataIndex: 'status',
      render: (v: AppKey['status']) => (v === 'enabled' ? <Tag color="green">已启用</Tag> : <Tag>已停用</Tag>),
    },
    { title: '创建人', dataIndex: 'created_by' },
    { title: '创建时间', dataIndex: 'created_at', render: fmtTime },
    {
      title: '操作',
      key: 'action',
      render: (_, k) =>
        k.status === 'enabled' ? (
          <Popconfirm
            title="停用这个 App Key？"
            description="停用后，带这个 App Key 的注册和上报都会被拒绝(403)。"
            okText="停用"
            cancelText="取消"
            onConfirm={() => setStatus(k, 'disabled')}
          >
            <Button type="link" size="small" danger>
              停用
            </Button>
          </Popconfirm>
        ) : (
          <Button type="link" size="small" onClick={() => setStatus(k, 'enabled')}>
            启用
          </Button>
        ),
    },
  ]

  return (
    <Flex vertical gap={16}>
      <Flex justify="space-between" align="center">
        <span style={{ color: '#999' }}>注册、上报带的 app_key 必须是这里已启用的 App Key，否则返回 403。</span>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreating(true)}>
          生成 App Key
        </Button>
      </Flex>
      {error && <Alert type="error" title={error} showIcon />}
      <Table<AppKey> rowKey="id" columns={columns} dataSource={data?.items} loading={loading} pagination={false} />

      <Modal
        title="生成 App Key"
        open={creating}
        okText="生成"
        cancelText="取消"
        confirmLoading={saving}
        onOk={() => form.submit()}
        onCancel={() => setCreating(false)}
        destroyOnHidden
      >
        <Form<CreateForm> form={form} layout="vertical" onFinish={create}>
          <Form.Item name="remark" label="备注" rules={[{ max: 255, message: '最多255个字' }]}>
            <Input placeholder="例如：阿福正式环境" />
          </Form.Item>
          <Form.Item
            name="app_key"
            label="导入已有的 App Key(可选)"
            extra="已经发给客户的 App Key 填在这里；留空则自动生成 AK- 开头的随机值。"
            rules={[{ pattern: /^[A-Za-z0-9_-]{1,64}$/, message: '只能包含字母、数字、- 和 _，最多64个字符' }]}
          >
            <Input placeholder="留空自动生成" />
          </Form.Item>
        </Form>
      </Modal>
    </Flex>
  )
}
