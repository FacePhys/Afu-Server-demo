import { useState } from 'react'
import { Alert, Card, Col, Empty, Flex, Row, Statistic } from 'antd'
import { Pie } from '@ant-design/plots'
import { apiGet } from '../api/client'
import type { DailyStats, Overview as OverviewData } from '../api/types'
import { useApi } from '../api/useApi'
import DailyCharts from '../components/DailyCharts'
import TimeRange, { initialRange } from '../components/TimeRange'
import { fmtDuration } from '../utils/format'
import { rangeParams } from '../utils/time'

export default function Overview() {
  const [range, setRange] = useState(() => initialRange('today'))
  const params = rangeParams(range)
  const overview = useApi(() => apiGet<OverviewData>('/overview', params), [params.from, params.to])
  const daily = useApi(() => apiGet<DailyStats>('/stats/daily', params), [params.from, params.to])
  const o = overview.data

  const kpis = [
    { title: '安装总数', value: o?.installs_total },
    { title: '新增安装', value: o?.installs_new },
    { title: '上传次数', value: o?.reports },
    { title: '计次', value: o?.measurements.count },
    { title: '测量时长', value: o ? fmtDuration(o.measurements.dt_ms) : undefined },
    { title: '异常计次(≤5秒)', value: o?.measurements.short_count },
  ]

  return (
    <Flex vertical gap={16}>
      <TimeRange defaultPreset="today" onChange={setRange} />
      {(overview.error || daily.error) && <Alert type="error" title={overview.error ?? daily.error} showIcon />}

      <Row gutter={[16, 16]}>
        {kpis.map((k) => (
          <Col key={k.title} xs={12} md={8} xl={4}>
            <Card loading={overview.loading}>
              <Statistic title={k.title} value={k.value ?? '-'} />
            </Card>
          </Col>
        ))}
      </Row>
      <Card size="small" loading={overview.loading}>
        换号：编号冲突 {o?.reassigned.conflict ?? '-'} / 未注册 {o?.reassigned.unregistered ?? '-'}
        <span style={{ color: '#999', marginLeft: 16 }}>安装总数、平台分布不受时间区间影响</span>
      </Card>

      <DailyCharts days={daily.data?.days} loading={daily.loading} />

      <Card title="平台分布" loading={overview.loading}>
        {o?.by_platform.length ? (
          <Pie data={o.by_platform} angleField="installs" colorField="platform" height={260} />
        ) : (
          <Empty />
        )}
      </Card>
    </Flex>
  )
}
