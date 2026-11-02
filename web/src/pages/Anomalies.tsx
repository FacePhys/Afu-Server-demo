import { useState } from 'react'
import { Button, DatePicker, Flex, Form, Input, Select, Tag } from 'antd'
import type { Dayjs } from 'dayjs'
import { apiGet, type Params } from '../api/client'
import type { AnomalyKinds } from '../api/types'
import { useApi } from '../api/useApi'
import AnomaliesTable from '../components/AnomaliesTable'
import { ANOMALY_KIND_COLOR, ANOMALY_KIND_LABEL, ANOMALY_KIND_OPTIONS, fmtNum } from '../utils/format'
import { rangeParams } from '../utils/time'

interface FilterForm {
  kind?: string
  install_id?: string
  app_key?: string
  platform?: string
  range?: [Dayjs, Dayjs]
}

// Anomalies 异常清单：服务端发现的异常按「类型 + 设备」记一行，重复出现时次数累加；只记录，不影响处理。
export default function Anomalies() {
  const [form] = Form.useForm<FilterForm>()
  const [filters, setFilters] = useState<Params>({})
  const [runs, setRuns] = useState(0) // 每点一次查询加1：表格重新挂载、按类型统计重新查
  const { kind, ...others } = filters
  const kinds = useApi(() => apiGet<AnomalyKinds>('/anomalies/kinds', others), [runs]) // 按类型统计不受类型筛选影响

  const apply = (v: FilterForm) => {
    setFilters({
      kind: v.kind,
      install_id: v.install_id?.trim(),
      app_key: v.app_key?.trim(),
      platform: v.platform,
      ...rangeParams(v.range),
    })
    setRuns((n) => n + 1)
  }
  // 点类型统计：只看这一类，再点一次取消
  const pickKind = (k: string | undefined) => {
    form.setFieldValue('kind', k)
    apply(form.getFieldsValue())
  }

  return (
    <Flex vertical gap={16}>
      <span style={{ color: '#999' }}>
        服务端发现的异常按「类型 + 设备」记一行，重复出现时次数累加，详情是最近一次的；只记录，不影响处理(该拒的请求照常拒，上报照常扣费)。
      </span>
      <Form<FilterForm> form={form} layout="inline" onFinish={apply} style={{ rowGap: 12 }}>
        <Form.Item name="kind">
          <Select placeholder="类型" allowClear style={{ width: 200 }} options={ANOMALY_KIND_OPTIONS} />
        </Form.Item>
        <Form.Item name="install_id">
          <Input placeholder="install_id(含换号后的编号)" allowClear style={{ width: 300 }} />
        </Form.Item>
        <Form.Item name="app_key">
          <Input placeholder="App Key" allowClear style={{ width: 200 }} />
        </Form.Item>
        <Form.Item name="platform">
          <Select
            placeholder="平台"
            allowClear
            style={{ width: 110 }}
            options={[
              { value: 'android', label: 'android' },
              { value: 'ios', label: 'ios' },
            ]}
          />
        </Form.Item>
        <Form.Item name="range">
          <DatePicker.RangePicker showTime placeholder={['发生时间起', '发生时间止']} />
        </Form.Item>
        <Button type="primary" htmlType="submit">
          查询
        </Button>
      </Form>
      <Flex gap={8} wrap align="center">
        <span>按类型：</span>
        {kinds.data?.kinds.map((k) => (
          <Tag.CheckableTag
            key={k.kind}
            checked={kind === k.kind}
            onChange={(on) => pickKind(on ? k.kind : undefined)}
            style={{ border: `1px solid ${kind === k.kind ? 'transparent' : '#d9d9d9'}` }}
          >
            <Tag color={ANOMALY_KIND_COLOR[k.kind]} style={{ marginInlineEnd: 4 }}>
              {ANOMALY_KIND_LABEL[k.kind] ?? k.kind}
            </Tag>
            {fmtNum(k.installs)} 台 / {fmtNum(k.count)} 次
          </Tag.CheckableTag>
        ))}
        {kinds.data && !kinds.data.kinds.length && <span style={{ color: '#999' }}>没有异常</span>}
      </Flex>
      <AnomaliesTable key={runs} filters={filters} />
    </Flex>
  )
}
