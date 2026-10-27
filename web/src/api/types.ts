// 与 internal/console/types.go 一一对应，接口说明见 docs/internal-console.md。

export interface Page<T> {
  items: T[]
  next_cursor: string // 没有下一页时为空字符串
}

export interface Me {
  name: string
}

export interface Meta {
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

/** 上报明文里的一条计次记录 */
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
  measurements: ReportedMeasurement[] // 本次上报带的计次记录；空上报为[]
  charge: number // 本次上报扣的钱，0.0001元；没扣费为0
  payload?: unknown // 原始明文，只有详情接口返回
}

export interface AppKey {
  id: number
  app_key: string
  remark: string
  status: 'enabled' | 'disabled'
  // 金额单位 0.0001 元，见 utils/money.ts
  unit_price: number // 单价/次，0为不收费
  balance: number // 余额，透支时为负
  overdraft_limit: number // 透支额度
  used_count: number // 累计计费的计次
  offline_runs: number // 离线可用次数：从已确认往后还能新增几次计次，cap_runs 直接下发
  billing_state: 'free' | 'normal' | 'overdraft' | 'exhausted'
  created_by: string
  created_at: string
  updated_at: string
}

export interface AppKeyList {
  items: AppKey[]
}

/** 计费流水，金额单位 0.0001 元 */
export interface LedgerEntry {
  id: number
  kind: 'consume' | 'recharge'
  amount: number // 扣费为负、充值为正
  balance_after: number
  count: number
  unit_price: number
  install_id: string
  seq: number
  operator: string
  remark: string
  created_at: string
}
