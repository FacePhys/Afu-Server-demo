/** 千分位 */
export function fmtNum(n: number | null | undefined): string {
  return n == null ? '-' : n.toLocaleString('zh-CN')
}

/** 时长：不到1分钟显示「9.97秒」，否则「1:56:16」 */
export function fmtDuration(ms: number): string {
  if (ms < 60_000) return `${(ms / 1000).toFixed(2)}秒`
  const total = Math.round(ms / 1000)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

/** 一条计次记录的时长不超过5秒算异常：窗口内测满5秒(不含)才计次，见 readme.md「使用规则 / 计次」第1条 */
export const SHORT_DT_MS = 5000

export const RESET_REASON_LABEL: Record<string, string> = {
  '': '首次',
  corrupt: '文件损坏',
  conflict: '编号冲突',
  unregistered: '未注册',
}

/** 筛选用：接口里 first 表示首次安装 */
export const RESET_REASON_OPTIONS = [
  { value: 'first', label: '首次' },
  { value: 'corrupt', label: '文件损坏' },
  { value: 'conflict', label: '编号冲突' },
  { value: 'unregistered', label: '未注册' },
]

/** 异常清单的类型，与 internal/anomaly 的 Kind* 一致 */
export const ANOMALY_KIND_LABEL: Record<string, string> = {
  time_reversed: '计次起止时间颠倒',
  bad_payload: '明文不合法(400)',
  invalid_app_key: 'App Key 无效(403)',
  reassign_conflict: '换号：注册编号冲突',
  reassign_unregistered: '换号：上报编号不存在',
  counters_regressed: '计数变小',
  sessions_over_cap: '新增计次超过离线次数',
}

export const ANOMALY_KIND_COLOR: Record<string, string> = {
  time_reversed: 'red',
  bad_payload: 'volcano',
  invalid_app_key: 'orange',
  reassign_conflict: 'blue',
  reassign_unregistered: 'geekblue',
  counters_regressed: 'purple',
  sessions_over_cap: 'gold',
}

export const ANOMALY_KIND_OPTIONS = Object.entries(ANOMALY_KIND_LABEL).map(([value, label]) => ({ value, label }))
