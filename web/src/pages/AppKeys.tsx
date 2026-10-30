import { useState } from 'react'
import {
  Alert,
  App,
  Button,
  Drawer,
  Flex,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Space,
  Table,
  Tag,
  Typography,
  type TableColumnsType,
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { Link } from 'react-router'
import { apiGet, apiSend, errorText } from '../api/client'
import type { AppKey, AppKeyList, LedgerEntry, Page } from '../api/types'
import { useApi } from '../api/useApi'
import PagedTable from '../components/PagedTable'
import { IdText } from '../components/Common'
import { fmtNum } from '../utils/format'
import { fmtYuan, unitsToYuan, yuanToUnits } from '../utils/money'
import { fmtTime } from '../utils/time'

interface ConfigForm {
  unit_price?: number | null
  overdraft_limit?: number | null
  offline_runs?: number | null
  max_violations?: number | null
}

interface CreateForm extends ConfigForm {
  remark?: string
  app_key?: string
  initial_balance?: number | null
}

interface RechargeForm {
  amount?: number | null
  remark?: string
}

const BILLING_TAG: Record<AppKey['billing_state'], { color?: string; text: string }> = {
  free: { text: '不收费' },
  normal: { color: 'green', text: '正常' },
  overdraft: { color: 'orange', text: '透支中' },
  exhausted: { color: 'red', text: '已停发' },
}

const yuanInput = { min: 0, precision: 4, style: { width: '100%' }, suffix: '元' }

/** 单价、透支额度、离线可用次数、违规上限，创建和修改配置共用 */
function ConfigFields() {
  return (
    <>
      <Form.Item name="unit_price" label="单价(每次计次)" extra="每次上报按「新增计次 × 单价」从余额扣；留空或填 0 表示不收费。">
        <InputNumber {...yuanInput} placeholder="0" />
      </Form.Item>
      <Form.Item
        name="overdraft_limit"
        label="透支额度"
        extra="余额可以扣到负数，最多透支这么多；余额加透支额度不够付下一次时停发额度(下发 cap_runs = 0)，SDK 收到后不能再开新窗口。"
      >
        <InputNumber {...yuanInput} placeholder="0" />
      </Form.Item>
      <Form.Item
        name="offline_runs"
        label="离线可用次数"
        extra="回复里的 cap_runs 直接下发这个值：设备不等服务端确认，最多能先用这么多次计次(断网时就靠它)，联网上报后按新增计次扣费。范围 0~16(一份上报最多 16 条)，填 0 相当于停发。"
        rules={[{ required: true, message: '请填写离线可用次数' }]}
      >
        <InputNumber min={0} max={16} precision={0} style={{ width: '100%' }} suffix="次" />
      </Form.Item>
      <Form.Item
        name="max_violations"
        label="违规上限"
        extra="设备上报的计次记录起始时间晚于结束时间(start_ts > end_ts)算一次违规，照常扣费；一台设备的违规次数达到上限后，给它的回复 cap_runs 为 0(停发)，在「安装」详情里清零后恢复。填 0 表示不限(只计数、不停发)，最大 1000。"
        rules={[{ required: true, message: '请填写违规上限' }]}
      >
        <InputNumber min={0} max={1000} precision={0} style={{ width: '100%' }} suffix="次" />
      </Form.Item>
    </>
  )
}

/** 计费流水：扣费、充值，新的在前 */
function LedgerDrawer({ k, onClose }: { k: AppKey; onClose: () => void }) {
  const columns: TableColumnsType<LedgerEntry> = [
    { title: '时间', dataIndex: 'created_at', render: fmtTime },
    {
      title: '类型',
      dataIndex: 'kind',
      render: (v: LedgerEntry['kind']) => (v === 'recharge' ? <Tag color="green">充值</Tag> : <Tag>扣费</Tag>),
    },
    {
      title: '金额(元)',
      dataIndex: 'amount',
      align: 'right',
      render: (v: number) => <span style={{ color: v > 0 ? '#389e0d' : undefined }}>{v > 0 ? `+${fmtYuan(v)}` : fmtYuan(v)}</span>,
    },
    { title: '变动后余额(元)', dataIndex: 'balance_after', align: 'right', render: fmtYuan },
    {
      title: '明细',
      key: 'detail',
      render: (_, e) =>
        e.kind === 'consume' ? (
          <Space size={4} wrap>
            <span>
              {fmtNum(e.count)}次 × {fmtYuan(e.unit_price)}元
            </span>
            <Link to={`/installs/${e.install_id}`}>
              <IdText id={e.install_id} />
            </Link>
            <span>seq {e.seq}</span>
          </Space>
        ) : (
          <span>
            {e.operator}
            {e.remark && `：${e.remark}`}
          </span>
        ),
    },
  ]
  return (
    <Drawer open title={`计费流水 ${k.app_key}`} size={900} onClose={onClose} destroyOnHidden>
      <PagedTable<LedgerEntry>
        rowKey={(e) => String(e.id)}
        columns={columns}
        fetchPage={(page) => apiGet<Page<LedgerEntry>>(`/app-keys/${k.id}/ledger`, page)}
      />
    </Drawer>
  )
}

// AppKeys App Key 管理：生成(或导入已发给客户的)、定价、充值、流水、停用 / 启用。注册、上报只认已启用的 App Key。
export default function AppKeys() {
  const { message } = App.useApp()
  const { data, loading, error, reload } = useApi(() => apiGet<AppKeyList>('/app-keys'), [])
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<AppKey>()
  const [recharging, setRecharging] = useState<AppKey>()
  const [ledgerOf, setLedgerOf] = useState<AppKey>()
  const [saving, setSaving] = useState(false)
  const [createForm] = Form.useForm<CreateForm>()
  const [configForm] = Form.useForm<ConfigForm>()
  const [rechargeForm] = Form.useForm<RechargeForm>()

  // save 统一处理保存中、成功提示、失败提示和刷新列表
  const save = async (fn: () => Promise<string>, done: () => void) => {
    setSaving(true)
    try {
      message.success(await fn())
      done()
      reload()
    } catch (e) {
      message.error(errorText(e))
    } finally {
      setSaving(false)
    }
  }

  const create = (v: CreateForm) =>
    save(
      async () => {
        const k = await apiSend<AppKey>('POST', '/app-keys', {
          remark: v.remark?.trim() ?? '',
          app_key: v.app_key?.trim() ?? '',
          unit_price: yuanToUnits(v.unit_price),
          initial_balance: yuanToUnits(v.initial_balance),
          overdraft_limit: yuanToUnits(v.overdraft_limit),
          offline_runs: v.offline_runs ?? 0,
          max_violations: v.max_violations ?? 0,
        })
        return `已创建 ${k.app_key}`
      },
      () => {
        setCreating(false)
        createForm.resetFields()
      },
    )

  const updateConfig = (v: ConfigForm) =>
    editing &&
    save(
      async () => {
        await apiSend<AppKey>('PATCH', `/app-keys/${editing.id}`, {
          unit_price: yuanToUnits(v.unit_price),
          overdraft_limit: yuanToUnits(v.overdraft_limit),
          offline_runs: v.offline_runs ?? 0,
          max_violations: v.max_violations ?? 0,
        })
        return '配置已修改'
      },
      () => setEditing(undefined),
    )

  const recharge = (v: RechargeForm) =>
    recharging &&
    save(
      async () => {
        const k = await apiSend<AppKey>('POST', `/app-keys/${recharging.id}/recharge`, {
          amount: yuanToUnits(v.amount),
          remark: v.remark?.trim() ?? '',
        })
        return `已充值，余额 ${fmtYuan(k.balance)} 元`
      },
      () => setRecharging(undefined),
    )

  const setStatus = (k: AppKey, status: AppKey['status']) =>
    save(async () => {
      await apiSend<AppKey>('PATCH', `/app-keys/${k.id}`, { status })
      return status === 'enabled' ? '已启用' : '已停用'
    }, () => {})

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
    { title: '单价(元/次)', dataIndex: 'unit_price', align: 'right', render: (v: number) => (v ? fmtYuan(v) : '-') },
    {
      title: '余额(元)',
      dataIndex: 'balance',
      align: 'right',
      render: (v: number) => <span style={{ color: v < 0 ? '#cf1322' : undefined }}>{fmtYuan(v)}</span>,
    },
    { title: '透支额度(元)', dataIndex: 'overdraft_limit', align: 'right', render: fmtYuan },
    { title: '计费次数', dataIndex: 'used_count', align: 'right', render: fmtNum },
    { title: '离线次数', dataIndex: 'offline_runs', align: 'right', render: fmtNum },
    { title: '违规上限', dataIndex: 'max_violations', align: 'right', render: (v: number) => (v ? fmtNum(v) : '不限') },
    {
      title: '计费',
      dataIndex: 'billing_state',
      render: (v: AppKey['billing_state']) => <Tag color={BILLING_TAG[v].color}>{BILLING_TAG[v].text}</Tag>,
    },
    { title: '创建人', dataIndex: 'created_by' },
    { title: '创建时间', dataIndex: 'created_at', render: fmtTime },
    {
      title: '操作',
      key: 'action',
      fixed: 'right',
      render: (_, k) => (
        <Space size={0}>
          <Button
            type="link"
            size="small"
            onClick={() => {
              rechargeForm.resetFields()
              setRecharging(k)
            }}
          >
            充值
          </Button>
          <Button
            type="link"
            size="small"
            onClick={() => {
              configForm.setFieldsValue({
                unit_price: k.unit_price ? unitsToYuan(k.unit_price) : null,
                overdraft_limit: unitsToYuan(k.overdraft_limit),
                offline_runs: k.offline_runs,
                max_violations: k.max_violations,
              })
              setEditing(k)
            }}
          >
            修改配置
          </Button>
          <Button type="link" size="small" onClick={() => setLedgerOf(k)}>
            流水
          </Button>
          {k.status === 'enabled' ? (
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
          )}
        </Space>
      ),
    },
  ]

  return (
    <Flex vertical gap={16}>
      <Flex justify="space-between" align="center">
        <span style={{ color: '#999' }}>
          注册、上报带的 app_key 必须是这里已启用的 App Key，否则返回 403。按次计费：每次上报按新增计次 × 单价从余额扣。
        </span>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreating(true)}>
          生成 App Key
        </Button>
      </Flex>
      {error && <Alert type="error" title={error} showIcon />}
      <Table<AppKey>
        rowKey="id"
        columns={columns}
        dataSource={data?.items}
        loading={loading}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />

      <Modal
        title="生成 App Key"
        open={creating}
        okText="生成"
        cancelText="取消"
        confirmLoading={saving}
        onOk={() => createForm.submit()}
        onCancel={() => setCreating(false)}
        destroyOnHidden
      >
        <Form<CreateForm> form={createForm} layout="vertical" onFinish={create} initialValues={{ offline_runs: 5, max_violations: 3 }}>
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
          <ConfigFields />
          <Form.Item name="initial_balance" label="初始余额" extra="会记一条充值流水；之后用「充值」追加。">
            <InputNumber {...yuanInput} placeholder="0" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={`修改配置 ${editing?.app_key ?? ''}`}
        open={!!editing}
        okText="保存"
        cancelText="取消"
        confirmLoading={saving}
        onOk={() => configForm.submit()}
        onCancel={() => setEditing(undefined)}
        destroyOnHidden
      >
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
          title="新单价从下一次上报开始扣费；新的离线可用次数、违规上限从下一次回复(ack / refused)开始生效；余额只能通过充值和扣费变化。"
        />
        <Form<ConfigForm> form={configForm} layout="vertical" onFinish={updateConfig}>
          <ConfigFields />
        </Form>
      </Modal>

      <Modal
        title={`充值 ${recharging?.app_key ?? ''}`}
        open={!!recharging}
        okText="充值"
        cancelText="取消"
        confirmLoading={saving}
        onOk={() => rechargeForm.submit()}
        onCancel={() => setRecharging(undefined)}
        destroyOnHidden
      >
        {recharging && (
          <Alert type="info" showIcon style={{ marginBottom: 16 }} title={`当前余额 ${fmtYuan(recharging.balance)} 元`} />
        )}
        <Form<RechargeForm> form={rechargeForm} layout="vertical" onFinish={recharge}>
          <Form.Item
            name="amount"
            label="充值金额"
            rules={[{ required: true, type: 'number', min: 0.0001, message: '充值金额必须大于0' }]}
          >
            <InputNumber {...yuanInput} placeholder="例如 100" />
          </Form.Item>
          <Form.Item name="remark" label="备注" rules={[{ max: 255, message: '最多255个字' }]}>
            <Input placeholder="例如：10月续费" />
          </Form.Item>
        </Form>
      </Modal>

      {ledgerOf && <LedgerDrawer k={ledgerOf} onClose={() => setLedgerOf(undefined)} />}
    </Flex>
  )
}
