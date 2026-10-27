// 金额：接口里一律是整数，单位 0.0001 元(1 元 = 10000)，和服务端 store.MoneyUnitsPerYuan 一致。
export const UNITS_PER_YUAN = 10000

/** 金额单位转成元的字符串：至少2位、最多4位小数，例如 5000 → "0.50"，-12345 → "-1.2345" */
export function fmtYuan(units: number): string {
  const s = (Math.abs(units) / UNITS_PER_YUAN).toFixed(4).replace(/(\.\d\d\d?)0+$/, '$1').replace(/(\.\d\d)0$/, '$1')
  return units < 0 ? `-${s}` : s
}

/** 元(输入框的值)转成金额单位；空值为0 */
export function yuanToUnits(yuan: number | null | undefined): number {
  return yuan ? Math.round(yuan * UNITS_PER_YUAN) : 0
}

/** 金额单位转成元(给输入框回填) */
export function unitsToYuan(units: number): number {
  return units / UNITS_PER_YUAN
}
