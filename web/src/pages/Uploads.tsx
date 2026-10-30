import { useState } from 'react'
import { Button, Checkbox, DatePicker, Flex, Form, Input } from 'antd'
import type { Dayjs } from 'dayjs'
import type { Params } from '../api/client'
import UploadsTable from '../components/UploadsTable'
import { SHORT_DT_MS } from '../utils/format'
import { rangeParams } from '../utils/time'

interface FilterForm {
  install_id?: string
  range?: [Dayjs, Dayjs]
  short_only?: boolean
}

// Uploads 上传记录：每行一次上传，本次计次和累计计数直接展示。
export default function Uploads() {
  const [filters, setFilters] = useState<Params>({})
  const [runs, setRuns] = useState(0) // 每点一次查询加1，表格重新挂载：条件没变也从第一页拿最新数据

  const onFinish = ({ range, install_id, short_only }: FilterForm) => {
    setFilters({
      install_id: install_id?.trim(),
      max_dt_ms: short_only ? SHORT_DT_MS : undefined,
      ...rangeParams(range),
    })
    setRuns((n) => n + 1)
  }

  return (
    <Flex vertical gap={16}>
      <Form<FilterForm> layout="inline" onFinish={onFinish} style={{ rowGap: 12 }}>
        <Form.Item name="install_id">
          <Input placeholder="install_id" allowClear style={{ width: 300 }} />
        </Form.Item>
        <Form.Item name="range">
          <DatePicker.RangePicker showTime placeholder={['收到时间起', '收到时间止']} />
        </Form.Item>
        <Form.Item name="short_only" valuePropName="checked">
          <Checkbox>只看含5秒及以内计次的</Checkbox>
        </Form.Item>
        <Button type="primary" htmlType="submit">
          查询
        </Button>
      </Form>
      <UploadsTable key={runs} filters={filters} />
    </Flex>
  )
}
