import { describe, expect, it } from 'vitest'
import dayjs from 'dayjs'
import { fmtDuration, fmtNum } from './format'
import { fmtYuan, unitsToYuan, yuanToUnits } from './money'
import { dayRange, fmtMs, fmtTime, presetRange, rangeParams, toParam, TZ } from './time'

describe('format', () => {
  it('时长', () => {
    expect(fmtDuration(9966)).toBe('9.97秒')
    expect(fmtDuration(59_999)).toBe('60.00秒')
    expect(fmtDuration(6_976_200)).toBe('1:56:16')
    expect(fmtDuration(60_000)).toBe('0:01:00')
  })
  it('千分位', () => {
    expect(fmtNum(1234567)).toBe('1,234,567')
    expect(fmtNum(undefined)).toBe('-')
  })
})

describe('time', () => {
  it('UTC转北京时间', () => {
    expect(fmtTime('2026-10-26T04:14:52.963Z')).toBe('2026-10-26 12:14:52')
    expect(fmtTime(null)).toBe('-')
    expect(fmtMs(Date.UTC(2026, 9, 25, 16, 30))).toBe('2026-10-26 00:30:00')
  })
  it('接口参数带+08:00', () => {
    expect(toParam(dayjs.tz('2026-10-26 00:00:00', TZ))).toBe('2026-10-26T00:00:00+08:00')
  })
  it('近7天从6天前的北京时间0点开始', () => {
    const now = dayjs('2026-10-26T04:00:00Z') // 北京时间12:00
    const [from, to] = presetRange(7, now)
    expect(toParam(from)).toBe('2026-10-20T00:00:00+08:00')
    expect(toParam(to)).toBe('2026-10-26T12:00:00+08:00')
  })
  it('自定义日期区间左闭右开', () => {
    const r = dayRange(dayjs.tz('2026-10-24 15:00:00', TZ), dayjs.tz('2026-10-26 09:00:00', TZ))
    expect(rangeParams(r)).toEqual({ from: '2026-10-24T00:00:00+08:00', to: '2026-10-27T00:00:00+08:00' })
  })
})


describe('money', () => {
  it('显示：至少2位、最多4位小数', () => {
    expect(fmtYuan(5000)).toBe('0.50')
    expect(fmtYuan(10000)).toBe('1.00')
    expect(fmtYuan(12345)).toBe('1.2345')
    expect(fmtYuan(12340)).toBe('1.234')
    expect(fmtYuan(-15001)).toBe('-1.5001')
    expect(fmtYuan(0)).toBe('0.00')
  })
  it('元和金额单位互转', () => {
    expect(yuanToUnits(0.5)).toBe(5000)
    expect(yuanToUnits(0.0001)).toBe(1)
    expect(yuanToUnits(19.99)).toBe(199900)
    expect(yuanToUnits(null)).toBe(0)
    expect(unitsToYuan(5000)).toBe(0.5)
  })
})
