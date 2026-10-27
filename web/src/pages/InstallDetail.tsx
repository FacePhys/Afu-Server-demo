import { Alert, Button, Card, Descriptions, Flex, Result, Spin } from 'antd'
import { ArrowLeftOutlined } from '@ant-design/icons'
import { useLocation, useNavigate, useParams } from 'react-router'
import { apiGet } from '../api/client'
import type { Install } from '../api/types'
import { useApi } from '../api/useApi'
import { IdText, ResetReasonTag } from '../components/Common'
import UploadsTable from '../components/UploadsTable'
import { fmtDuration, fmtNum } from '../utils/format'
import { fmtTime } from '../utils/time'

export default function InstallDetail() {
  const { installId = '' } = useParams()
  const navigate = useNavigate()
  const location = useLocation()
  const { data: d, loading, error } = useApi(() => apiGet<Install>(`/installs/${installId}`), [installId])

  // 从后台其他页面点进来的回到上一页；直接打开链接(没有站内历史)的回到安装列表
  const back = () => (location.key !== 'default' ? navigate(-1) : navigate('/installs'))
  const backButton = (
    <Button icon={<ArrowLeftOutlined />} onClick={back} style={{ alignSelf: 'flex-start' }}>
      返回
    </Button>
  )

  if (loading) return <Spin />
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
            { label: '测量次数', children: fmtNum(c.sessions) },
            { label: '有效帧数', children: fmtNum(c.frames_ok) },
            { label: 'run次数', children: fmtNum(c.run_ok) },
            { label: '测量时长', children: fmtDuration(c.dt_ms) },
            { label: '单调时钟时长', children: fmtDuration(c.mono_ms) },
            { label: '被拦截帧数', children: fmtNum(c.gated_frames) },
            { label: '被拦截run', children: fmtNum(c.gated_runs) },
          ]}
        />
      </Card>
      <Card title="上传记录">
        <UploadsTable filters={{ install_id: d.install_id }} showInstall={false} />
      </Card>
    </Flex>
  )
}
