import { Card, Col, Empty, Row } from 'antd'
import { Column } from '@ant-design/plots'
import type { DayStat } from '../api/types'

interface Point {
  date: string // MM-DD
  value: number
}

function DayColumn({ data }: { data: Point[] }) {
  if (!data.length) return <Empty />
  // 用柱状图：只有一天时折线画不出来；日期标签放不下时自动隐藏
  return (
    <Column
      data={data}
      xField="date"
      yField="value"
      height={240}
      style={{ maxWidth: 48 }} // 天数少时柱子不要撑满整行
      axis={{ x: { labelAutoHide: true, labelAutoRotate: false } }}
    />
  )
}

// DailyCharts 按天的测量次数和测量时长(分钟)，两张图并排。
export default function DailyCharts({ days, loading }: { days: DayStat[] | undefined; loading: boolean }) {
  const label = (d: DayStat) => d.date.slice(5)
  const counts = (days ?? []).map((d) => ({ date: label(d), value: d.measurements }))
  const minutes = (days ?? []).map((d) => ({ date: label(d), value: Number((d.dt_ms / 60000).toFixed(1)) }))

  return (
    <Row gutter={[16, 16]}>
      <Col xs={24} lg={12}>
        <Card title="测量次数(按天)" loading={loading}>
          <DayColumn data={counts} />
        </Card>
      </Col>
      <Col xs={24} lg={12}>
        <Card title="测量时长(分钟，按天)" loading={loading}>
          <DayColumn data={minutes} />
        </Card>
      </Col>
    </Row>
  )
}
