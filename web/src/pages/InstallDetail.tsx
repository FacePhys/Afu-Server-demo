import { useState } from 'react'
import { Alert, App, Button, Card, Descriptions, Flex, Popconfirm, Result, Spin, Tag, Typography } from 'antd'
import { ArrowLeftOutlined } from '@ant-design/icons'
import { useLocation, useNavigate, useParams } from 'react-router'
import { apiGet, apiSend, errorText } from '../api/client'
import type { Install, Violations } from '../api/types'
import { useApi } from '../api/useApi'
import { IdText, ResetReasonTag } from '../components/Common'
import UploadsTable from '../components/UploadsTable'
import { fmtDuration, fmtNum } from '../utils/format'
import { fmtTime } from '../utils/time'

/** 违规：次数、上限、是否停发；清零后从下一次回复开始恢复下发 */
function ViolationsCard({ installId, v, onReset }: { installId: string; v: Violations; onReset: () => void }) {
  const { message } = App.useApp()
  const [resetting, setResetting] = useState(false)
  const reset = async () => {
    setResetting(true)
    try {
      await apiSend<Install>('POST', `/installs/${installId}/violations/reset`, {})
      message.success('违规次数已清零')
      onReset()
    } catch (e) {
      message.error(errorText(e))
    } finally {
      setResetting(false)
    }
  }
  return (
    <Card
      title="违规"
      extra={
        <Popconfirm
          title="清零这台设备的违规次数？"
          description="从下一次回复开始恢复下发 cap_runs；历史累计不变。"
          okText="清零"
          cancelText="取消"
          onConfirm={reset}
          disabled={!v.count}
        >
          <Button danger disabled={!v.count} loading={resetting}>
            清零
          </Button>
        </Popconfirm>
      }
    >
      <Typography.Paragraph type="secondary">
        计次记录的起始时间晚于结束时间(start_ts &gt; end_ts)算一次违规，照常扣费；违规次数达到 App Key
        的违规上限后停发(回复的 cap_runs 为 0)，清零后恢复。
      </Typography.Paragraph>
      <Descriptions
        column={{ xs: 1, md: 3 }}
        items={[
          { label: '当前违规次数', children: fmtNum(v.count) },
          { label: '违规上限', children: v.limit ? `${v.limit} 次` : '不限' },
          { label: '状态', children: v.stopped ? <Tag color="red">已停发</Tag> : <Tag color="green">正常下发</Tag> },
          { label: '历史累计', children: fmtNum(v.total) },
          {
            label: '最近一次违规',
            children: v.last_at ? `${fmtTime(v.last_at)}(seq ${v.last_seq}，计次 #${v.last_no})` : '-',
          },
          { label: '最近清零', children: v.reset_at ? `${fmtTime(v.reset_at)}(${v.reset_by})` : '-' },
        ]}
      />
    </Card>
  )
}

export default function InstallDetail() {
  const { installId = '' } = useParams()
  const navigate = useNavigate()
  const location = useLocation()
  const { data: d, loading, error, reload } = useApi(() => apiGet<Install>(`/installs/${installId}`), [installId])

  // 从后台其他页面点进来的回到上一页；直接打开链接(没有站内历史)的回到安装列表
  const back = () => (location.key !== 'default' ? navigate(-1) : navigate('/installs'))
  const backButton = (
    <Button icon={<ArrowLeftOutlined />} onClick={back} style={{ alignSelf: 'flex-start' }}>
      返回
    </Button>
  )

  if (loading && d?.install_id !== installId) return <Spin /> // 清零后刷新时保留原页面，不闪成加载中
  if (error === '记录不存在') {
    return (
      <Flex vertical gap={16}>
        {backButton}
        <Result status="404" title="安装记录不存在" />
      </Flex>
    )
  }
  if (error || !d) {
    return (
      <Flex vertical gap={16}>
        {backButton}
        <Alert type="error" title={error} showIcon />
      </Flex>
    )
  }

  const c = d.counters
  return (
    <Flex vertical gap={16}>
      {backButton}
      <Card>
        <Descriptions
          title={<IdText id={d.install_id} />}
          column={{ xs: 1, md: 3 }}
          items={[
            { label: 'app_id', children: d.app_id },
            { label: '平台', children: d.platform },
            { label: 'sdk', children: d.sdk },
            { label: '新安装原因', children: <ResetReasonTag reason={d.reset_reason} /> },
            { label: '创建时间', children: fmtTime(d.created_at) },
            { label: '最近更新', children: fmtTime(d.updated_at) },
          ]}
        />
      </Card>
      <Card title="已确认状态">
        <Descriptions
          column={{ xs: 2, md: 4 }}
          items={[
            { label: '已确认seq', children: d.confirmed_seq },
            { label: '累计计次', children: fmtNum(c.sessions) },
            { label: '有效帧数', children: fmtNum(c.frames_ok) },
            { label: 'run次数', children: fmtNum(c.run_ok) },
            { label: '测量时长', children: fmtDuration(c.dt_ms) },
            { label: '单调时钟时长', children: fmtDuration(c.mono_ms) },
            { label: '被拦截帧数', children: fmtNum(c.gated_frames) },
            { label: '被拦截run', children: fmtNum(c.gated_runs) },
          ]}
        />
      </Card>
      <ViolationsCard installId={d.install_id} v={d.violations} onReset={reload} />
      <Card title="上传记录">
        <UploadsTable filters={{ install_id: d.install_id }} showInstall={false} />
      </Card>
    </Flex>
  )
}
