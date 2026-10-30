import { useState } from 'react'
import { Button, DatePicker, Flex, Form, Input, Select, type TableColumnsType } from 'antd'
import { UnorderedListOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router'
import type { Dayjs } from 'dayjs'
import { apiGet, type Params } from '../api/client'
import type { Install, Page } from '../api/types'
import PagedTable from '../components/PagedTable'
import { IdText, ResetReasonTag, ViolationsText } from '../components/Common'
import { RESET_REASON_OPTIONS, fmtDuration, fmtNum } from '../utils/format'
import { fmtTime, rangeParams } from '../utils/time'

interface FilterForm {
  install_id?: string
  platform?: string
  reset_reason?: string
  violation?: string
  created?: [Dayjs, Dayjs]
}

const VIOLATION_OPTIONS = [
  { value: 'any', label: '有违规(未清零)' },
  { value: 'stopped', label: '违规已停发' },
]

export default function Installs() {
  const navigate = useNavigate()
  const [filters, setFilters] = useState<Params>({})
  const [runs, setRuns] = useState(0) // 每点一次查询加1，表格重新挂载：条件没变也从第一页拿最新数据
  const open = (id: string) => navigate(`/installs/${id}`)

  const columns: TableColumnsType<Install> = [
    {
      title: 'install_id',
      dataIndex: 'install_id',
      // 点复制按钮不触发整行跳转
      render: (v: string) => (
        <span onClick={(e) => e.stopPropagation()}>
          <IdText id={v} />
        </span>
      ),
    },
    { title: 'app_id', dataIndex: 'app_id' },
    { title: '平台', dataIndex: 'platform' },
    { title: 'sdk', dataIndex: 'sdk' },
    { title: '新安装原因', dataIndex: 'reset_reason', render: (v: string) => <ResetReasonTag reason={v} /> },
    { title: '已确认seq', dataIndex: 'confirmed_seq', align: 'right' },
    { title: '累计计次', key: 'sessions', align: 'right', render: (_, r) => fmtNum(r.counters.sessions) },
    { title: '测量时长', key: 'dt_ms', align: 'right', render: (_, r) => fmtDuration(r.counters.dt_ms) },
    { title: '违规(次数 / 上限)', key: 'violations', render: (_, r) => <ViolationsText v={r.violations} /> },
    { title: '创建时间', dataIndex: 'created_at', render: fmtTime },
    {
      title: '操作',
      key: 'action',
      fixed: 'right',
      render: (_, r) => (
        <Button
          type="link"
          size="small"
          icon={<UnorderedListOutlined />}
          onClick={(e) => {
            e.stopPropagation()
            open(r.install_id)
          }}
        >
          查看上传记录
        </Button>
      ),
    },
  ]

  const onFinish = ({ created, ...rest }: FilterForm) => {
    setFilters({ ...rest, install_id: rest.install_id?.trim(), ...rangeParams(created) })
    setRuns((n) => n + 1)
  }

  return (
    <Flex vertical gap={16}>
      <Form<FilterForm> layout="inline" onFinish={onFinish} style={{ rowGap: 12 }}>
        <Form.Item name="install_id">
          <Input placeholder="install_id" allowClear style={{ width: 300 }} />
        </Form.Item>
        <Form.Item name="platform">
          <Select
            placeholder="平台"
            allowClear
            style={{ width: 120 }}
            options={[
              { value: 'android', label: 'android' },
              { value: 'ios', label: 'ios' },
            ]}
          />
        </Form.Item>
        <Form.Item name="reset_reason">
          <Select placeholder="新安装原因" allowClear style={{ width: 140 }} options={RESET_REASON_OPTIONS} />
        </Form.Item>
        <Form.Item name="violation">
          <Select placeholder="违规" allowClear style={{ width: 150 }} options={VIOLATION_OPTIONS} />
        </Form.Item>
        <Form.Item name="created">
          <DatePicker.RangePicker showTime placeholder={['创建时间起', '创建时间止']} />
        </Form.Item>
        <Button type="primary" htmlType="submit">
          查询
        </Button>
      </Form>
      <PagedTable<Install>
        key={runs}
        rowKey="install_id"
        columns={columns}
        fetchPage={(page) => apiGet<Page<Install>>('/installs', { ...filters, ...page })}
        onRowClick={(r) => open(r.install_id)}
      />
    </Flex>
  )
}
