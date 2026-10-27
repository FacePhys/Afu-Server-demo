// 与 internal/console/types.go 一一对应，接口说明见 docs/internal-console.md。

export interface Page<T> {
  items: T[]
  next_cursor: string // 没有下一页时为空字符串
}

export interface Me {
  name: string
}

export interface Meta {
  per_measure_frames: number
  per_measure_runs: number
  ttl_sec: number
  report_sec: number
  active_kids: number[]
  version: string
}

export interface Overview {
  installs_total: number
  installs_new: number
  reassigned: { conflict: number; unregistered: number }
  reports: number
  measurements: { count: number; frames: number; dt_ms: number; short_count: number }
  by_platform: { platform: string; installs: number }[]
}

export interface DayStat {
  date: string
  new_installs: number
  reports: number
  measurements: number
  frames: number
  dt_ms: number
}

export interface DailyStats {
  days: DayStat[]
}

export interface Counters {
  sessions: number
  frames_ok: number
  dt_ms: number
  mono_ms: number
  run_ok: number
  gated_frames: number
  gated_runs: number
}

export interface Install {
  install_id: string
  app_id: string
  platform: string
  sdk: string
  reset_reason: string
  confirmed_seq: number
  counters: Counters
  created_at: string
  updated_at: string
}

/** 上报明文里的单次测量 */
export interface ReportedMeasurement {
  no: number
  start_ts: number
  end_ts: number
  frames: number
  dt_ms: number
}

export interface Report {
  id: number
  install_id: string
  seq: number
  client_ts: number
  received_at: string
  counters: Counters // 从明文解析，累计值
  measurements: ReportedMeasurement[] // 本次上报带的测量；空上报为[]
  payload?: unknown // 原始明文，只有详情接口返回
}

export interface AppKey {
  id: number
  app_key: string
  remark: string
  status: 'enabled' | 'disabled'
  created_by: string
  created_at: string
  updated_at: string
}

export interface AppKeyList {
  items: AppKey[]
}
