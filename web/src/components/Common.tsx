import { App, Button, Flex, Tag, Typography } from 'antd'
import { RESET_REASON_LABEL, SHORT_DT_MS, fmtDuration } from '../utils/format'

/** install_id：等宽字体 + 复制 */
export function IdText({ id }: { id: string }) {
  return (
    <Typography.Text code copyable={{ text: id }} style={{ whiteSpace: 'nowrap' }}>
      {id}
    </Typography.Text>
  )
}

/** 新安装原因；换号产生的记录标蓝色 */
export function ResetReasonTag({ reason }: { reason: string }) {
  const reassigned = reason === 'conflict' || reason === 'unregistered'
  return <Tag color={reassigned ? 'blue' : undefined}>{RESET_REASON_LABEL[reason] ?? reason}</Tag>
}

/** 计次记录的时长；不超过5秒的标橙色(SDK 不该发) */
export function DurationText({ ms }: { ms: number }) {
  return ms <= SHORT_DT_MS ? <Tag color="orange">{fmtDuration(ms)}</Tag> : <span>{fmtDuration(ms)}</span>
}

/** 原始明文：纯文本格式化显示，不解析成HTML */
export function JsonView({ value }: { value: unknown }) {
  const { message } = App.useApp()
  const text = JSON.stringify(value, null, 2)
  return (
    <Flex vertical gap={8}>
      <Flex justify="flex-end">
        <Button
          size="small"
          onClick={() => navigator.clipboard.writeText(text).then(() => message.success('已复制'))}
        >
          复制
        </Button>
      </Flex>
      <pre style={{ margin: 0, padding: 12, background: '#fafafa', borderRadius: 6, overflow: 'auto', fontSize: 12 }}>
        {text}
      </pre>
    </Flex>
  )
}
