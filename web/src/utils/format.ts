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

/** 测量时长不超过5秒算「短测量」，见 readme.md「使用规则」第2条 */
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
