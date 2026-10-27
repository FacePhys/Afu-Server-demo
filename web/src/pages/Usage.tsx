import { useState } from 'react'
import { Alert, Flex, Table, type TableColumnsType } from 'antd'
import { apiGet } from '../api/client'
import type { DailyStats, DayStat } from '../api/types'
import { useApi } from '../api/useApi'
import DailyCharts from '../components/DailyCharts'
import TimeRange, { initialRange } from '../components/TimeRange'
import { fmtDuration, fmtNum } from '../utils/format'
import { rangeParams } from '../utils/time'

const columns: TableColumnsType<DayStat> = [
  { title: '日期', dataIndex: 'date' },
  { title: '新增安装', dataIndex: 'new_installs', align: 'right', render: fmtNum },
  { title: '上传次数', dataIndex: 'reports', align: 'right', render: fmtNum },
  { title: '测量次数', dataIndex: 'measurements', align: 'right', render: fmtNum },
  { title: '有效帧数', dataIndex: 'frames', align: 'right', render: fmtNum },
  { title: '测量时长', dataIndex: 'dt_ms', align: 'right', render: fmtDuration },
]

// Usage 用量统计：目前只有一个客户，按天看整体用量。
export default function Usage() {
  const [range, setRange] = useState(() => initialRange('30d'))
  const params = rangeParams(range)
  const { data, loading, error } = useApi(() => apiGet<DailyStats>('/stats/daily', params), [params.from, params.to])
  const days = data?.days ?? []

  // 区间合计放在表尾
  const total = days.reduce(
    (t, d) => ({
      new_installs: t.new_installs + d.new_installs,
      reports: t.reports + d.reports,
      measurements: t.measurements + d.measurements,
      frames: t.frames + d.frames,
      dt_ms: t.dt_ms + d.dt_ms,
    }),
    { new_installs: 0, reports: 0, measurements: 0, frames: 0, dt_ms: 0 },
  )

  return (
    <Flex vertical gap={16}>
      <TimeRange defaultPreset="30d" onChange={setRange} />
      {error && <Alert type="error" title={error} showIcon />}
      <DailyCharts days={days} loading={loading} />
      <Table<DayStat>
        rowKey="date"
        columns={columns}
        dataSource={[...days].reverse()} // 新的日期在前
        loading={loading}
        pagination={false}
        size="middle"
        summary={() => (
          <Table.Summary.Row>
            <Table.Summary.Cell index={0}>合计</Table.Summary.Cell>
            <Table.Summary.Cell index={1} align="right">{fmtNum(total.new_installs)}</Table.Summary.Cell>
            <Table.Summary.Cell index={2} align="right">{fmtNum(total.reports)}</Table.Summary.Cell>
            <Table.Summary.Cell index={3} align="right">{fmtNum(total.measurements)}</Table.Summary.Cell>
            <Table.Summary.Cell index={4} align="right">{fmtNum(total.frames)}</Table.Summary.Cell>
            <Table.Summary.Cell index={5} align="right">{fmtDuration(total.dt_ms)}</Table.Summary.Cell>
          </Table.Summary.Row>
        )}
      />
    </Flex>
  )
}
