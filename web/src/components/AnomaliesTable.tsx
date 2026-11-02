import { Tag, Typography, type TableColumnsType } from 'antd'
import { Link } from 'react-router'
import { apiGet, type Params } from '../api/client'
import type { Anomaly, Page } from '../api/types'
import { ANOMALY_KIND_COLOR, ANOMALY_KIND_LABEL, fmtNum } from '../utils/format'
import { fmtTime } from '../utils/time'
import PagedTable from './PagedTable'
import { IdText } from './Common'

/** 异常类型标签 */
export function AnomalyKindTag({ kind }: { kind: string }) {
  return <Tag color={ANOMALY_KIND_COLOR[kind]}>{ANOMALY_KIND_LABEL[kind] ?? kind}</Tag>
}

function InstallLink({ id }: { id: string }) {
  return (
    <Link to={`/installs/${id}`}>
      <IdText id={id} />
    </Link>
  )
}

const columns: TableColumnsType<Anomaly> = [
  { title: '类型', dataIndex: 'kind', render: (v: string) => <AnomalyKindTag kind={v} /> },
  {
    title: '设备(install_id)',
    dataIndex: 'install_id',
    render: (v: string, a) => {
      if (!v) return <Typography.Text type="secondary">编号不合法</Typography.Text>
      // 上报时编号不存在：服务端没有这条安装记录，不放链接
      return a.kind === 'reassign_unregistered' ? <IdText id={v} /> : <InstallLink id={v} />
    },
  },
  { title: '次数', dataIndex: 'count', align: 'right', render: fmtNum },
  { title: '最近发生', dataIndex: 'last_at', render: fmtTime },
  {
    title: '详情(最近一次)',
    dataIndex: 'detail',
    render: (v: string) => <div style={{ maxWidth: 440, whiteSpace: 'normal', wordBreak: 'break-all' }}>{v || '-'}</div>,
  },
  { title: '换号后的编号', dataIndex: 'related_id', render: (v: string) => (v ? <InstallLink id={v} /> : '-') },
  {
    title: 'App Key',
    dataIndex: 'app_key',
    render: (v: string) => (v ? <Typography.Text code>{v}</Typography.Text> : '-'),
  },
  { title: '平台 / sdk', key: 'client', render: (_, a) => [a.platform, a.sdk].filter(Boolean).join(' / ') || '-' },
  { title: '最近 seq', dataIndex: 'seq', align: 'right', render: (v: number) => v || '-' },
  { title: '首次发生', dataIndex: 'first_at', render: fmtTime },
]

/** 异常清单表格：每行是一台设备的一类异常，按最近发生时间倒序；filters 变化时由父组件换 key */
export default function AnomaliesTable({ filters }: { filters: Params }) {
  return (
    <PagedTable<Anomaly>
      rowKey={(a) => String(a.id)}
      columns={columns}
      fetchPage={(page) => apiGet<Page<Anomaly>>('/anomalies', { ...filters, ...page })}
    />
  )
}
