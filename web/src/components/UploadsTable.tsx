import { useState } from 'react'
import { Alert, Button, Drawer, Flex, Spin, Tag, Tooltip, type TableColumnsType } from 'antd'
import { Link } from 'react-router'
import { apiGet, type Params } from '../api/client'
import type { Page, Report, ReportedMeasurement } from '../api/types'
import { useApi } from '../api/useApi'
import { fmtDuration, fmtNum } from '../utils/format'
import { fmtMs, fmtTime } from '../utils/time'
import CursorTable from './CursorTable'
import { DurationText, IdText, JsonView } from './Common'

/** 客户端毫秒时间戳：显示北京时间，悬停看原始值 */
function ClientTime({ ms }: { ms: number }) {
  return (
    <Tooltip title={ms}>
      <span>{fmtMs(ms)}</span>
    </Tooltip>
  )
}

/** 本次上报带的测量：每条一行，悬停看客户端起止时间；空上报标灰 */
function ReportedMeasurements({ items }: { items: ReportedMeasurement[] }) {
  if (!items.length) return <Tag>空上报</Tag>
  return (
    <Flex vertical gap={4}>
      {items.map((m) => (
        <Tooltip key={m.no} title={`${fmtMs(m.start_ts)} ~ ${fmtMs(m.end_ts)}`}>
          <Flex gap={8} align="center" style={{ whiteSpace: 'nowrap' }}>
            <span>#{m.no}</span>
            <span>{fmtNum(m.frames)}帧</span>
            <DurationText ms={m.dt_ms} />
          </Flex>
        </Tooltip>
      ))}
    </Flex>
  )
}

function ReportDrawer({ id, onClose }: { id: number; onClose: () => void }) {
  const { data, loading, error } = useApi(() => apiGet<Report>(`/reports/${id}`), [id])
  return (
    <Drawer open title={`上传明文 #${id}`} size={640} onClose={onClose} destroyOnHidden>
      {error && <Alert type="error" title={error} showIcon />}
      {loading ? <Spin /> : data && <JsonView value={data.payload} />}
    </Drawer>
  )
}

/** 上传记录表格：每行一次上传(本次测量 + 累计计数)；filters 变化时由父组件换 key */
export default function UploadsTable({ filters, showInstall = true }: { filters: Params; showInstall?: boolean }) {
  const [openId, setOpenId] = useState<number>()
  const columns: TableColumnsType<Report> = [
    ...(showInstall
      ? [{ title: 'install_id', dataIndex: 'install_id', render: (v: string) => <Link to={`/installs/${v}`}><IdText id={v} /></Link> }]
      : []),
    { title: 'seq', dataIndex: 'seq' },
    { title: '客户端时间', dataIndex: 'client_ts', render: (v: number) => <ClientTime ms={v} /> },
    { title: '服务端收到时间', dataIndex: 'received_at', render: fmtTime },
    { title: '本次测量', key: 'measurements', render: (_, r) => <ReportedMeasurements items={r.measurements} /> },
    { title: '累计测量次数', key: 'sessions', align: 'right', render: (_, r) => fmtNum(r.counters.sessions) },
    { title: '累计有效帧数', key: 'frames_ok', align: 'right', render: (_, r) => fmtNum(r.counters.frames_ok) },
    {
      title: '累计时长',
      key: 'dt_ms',
      align: 'right',
      render: (_, r) => (
        <Tooltip title={`单调时钟：${fmtDuration(r.counters.mono_ms)}`}>
          <span>{fmtDuration(r.counters.dt_ms)}</span>
        </Tooltip>
      ),
    },
    { title: '累计run', key: 'run_ok', align: 'right', render: (_, r) => fmtNum(r.counters.run_ok) },
    {
      title: '被拦截(帧 / run)',
      key: 'gated',
      align: 'right',
      render: (_, r) => `${fmtNum(r.counters.gated_frames)} / ${fmtNum(r.counters.gated_runs)}`,
    },
    {
      title: '操作',
      key: 'action',
      render: (_, r) => (
        <Button type="link" size="small" onClick={() => setOpenId(r.id)}>
          查看明文
        </Button>
      ),
    },
  ]
  return (
    <>
      <CursorTable<Report>
        rowKey={(r) => String(r.id)}
        columns={columns}
        fetchPage={(cursor) => apiGet<Page<Report>>('/reports', { ...filters, cursor })}
      />
      {openId !== undefined && <ReportDrawer id={openId} onClose={() => setOpenId(undefined)} />}
    </>
  )
}
