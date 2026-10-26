import dayjs, { type Dayjs } from 'dayjs'
import utc from 'dayjs/plugin/utc'
import timezone from 'dayjs/plugin/timezone'
import 'dayjs/locale/zh-cn'

dayjs.extend(utc)
dayjs.extend(timezone)
dayjs.locale('zh-cn')

// 页面统一按北京时间显示，和服务端按天统计的口径一致
export const TZ = 'Asia/Shanghai'

export type Range = [Dayjs, Dayjs]

const DISPLAY = 'YYYY-MM-DD HH:mm:ss'

/** 服务端返回的RFC3339(UTC)转北京时间 */
export function fmtTime(iso: string | null | undefined): string {
  return iso ? dayjs(iso).tz(TZ).format(DISPLAY) : '-'
}

/** 客户端上报的毫秒时间戳转北京时间 */
export function fmtMs(ms: number): string {
  return dayjs(ms).tz(TZ).format(DISPLAY)
}

/** 把选择器里的时间按北京时间的钟面值转成接口参数(RFC3339，带+08:00) */
export function toParam(d: Dayjs): string {
  return dayjs.tz(d.format('YYYY-MM-DDTHH:mm:ss'), TZ).format()
}

/** 最近days天：从(今天 - days + 1)的北京时间0点到现在 */
export function presetRange(days: number, now: Dayjs = dayjs()): Range {
  const n = now.tz(TZ)
  return [n.startOf('day').subtract(days - 1, 'day'), n]
}

/** 自定义日期区间：开始日0点到结束日次日0点(左闭右开) */
export function dayRange(start: Dayjs, end: Dayjs): Range {
  return [start.startOf('day'), end.add(1, 'day').startOf('day')]
}

export function rangeParams(r: Range | null | undefined): { from?: string; to?: string } {
  return r ? { from: toParam(r[0]), to: toParam(r[1]) } : {}
}
