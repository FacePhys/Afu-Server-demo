import { useState } from 'react'
import { Alert, Flex, Table, type TableColumnsType } from 'antd'
import { apiGet } from '../api/client'
import type { AppKeyStat, AppKeyStats, DailyStats } from '../api/types'
import { useApi } from '../api/useApi'
import DailyCharts from '../components/DailyCharts'
import TimeRange, { initialRange } from '../components/TimeRange'
import { fmtDuration, fmtNum } from '../utils/format'
import { fmtTime, rangeParams } from '../utils/time'

function AppKeyDaily({ appKey, params }: { appKey: string; params: { from?: string; to?: string } }) {
  const { data, loading, error } = useApi(
    () => apiGet<DailyStats>('/stats/daily', { ...params, app_key: appKey }),
    [appKey, params.from, params.to],
  )
  return error ? <Alert type="error" title={error} showIcon /> : <DailyCharts days={data?.days} loading={loading} />
}

const columns: TableColumnsType<AppKeyStat> = [
  { title: 'app_key', dataIndex: 'app_key' },
  { title: '安装数(全部)', dataIndex: 'installs', align: 'right', render: fmtNum },
  { title: '活跃安装', dataIndex: 'active_installs', align: 'right', render: fmtNum },
  { title: '测量次数', dataIndex: 'measurements', align: 'right', render: fmtNum },
  { title: '有效帧数', dataIndex: 'frames', align: 'right', render: fmtNum },
  { title: '测量时长', dataIndex: 'dt_ms', align: 'right', render: fmtDuration },
  { title: '最近上传', dataIndex: 'last_report_at', render: fmtTime },
]

export default function Usage() {
  const [range, setRange] = useState(() => initialRange('30d'))
  const params = rangeParams(range)
  const { data, loading, error } = useApi(() => apiGet<AppKeyStats>('/stats/app-keys', params), [params.from, params.to])

  return (
    <Flex vertical gap={16}>
      <TimeRange defaultPreset="30d" onChange={setRange} />
      {error && <Alert type="error" title={error} showIcon />}
      <Table<AppKeyStat>
        rowKey="app_key"
        columns={columns}
        dataSource={data?.items}
        loading={loading}
        pagination={false}
        expandable={{ expandedRowRender: (r) => <AppKeyDaily appKey={r.app_key} params={params} /> }}
      />
    </Flex>
  )
}
