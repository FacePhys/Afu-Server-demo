const TOKEN_KEY = 'afu_console_token'
const API_PREFIX = '/afu/internal/api/v1'

// Token存在sessionStorage：关闭标签页即失效
export const tokenStore = {
  get: () => sessionStorage.getItem(TOKEN_KEY),
  set: (token: string) => sessionStorage.setItem(TOKEN_KEY, token),
  clear: () => sessionStorage.removeItem(TOKEN_KEY),
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, msg: string) {
    super(msg)
    this.status = status
    this.code = code
  }
}

export type Params = Record<string, string | number | boolean | undefined | null>

interface Options {
  token?: string // 登录页校验时传入，不读存储
  redirectOn401?: boolean // 默认 true：Token失效时清掉并跳登录页
}

export async function apiGet<T>(path: string, params?: Params, opts: Options = {}): Promise<T> {
  const url = new URL(API_PREFIX + path, window.location.origin)
  for (const [k, v] of Object.entries(params ?? {})) {
    if (v !== undefined && v !== null && v !== '' && v !== false) url.searchParams.set(k, String(v))
  }
  const token = opts.token ?? tokenStore.get() ?? ''
  const resp = await fetch(url, { headers: { Authorization: `Bearer ${token}` } })

  if (resp.status === 401 && (opts.redirectOn401 ?? true)) {
    tokenStore.clear()
    window.location.assign('/login')
  }
  const body = await resp.json().catch(() => ({}))
  if (!resp.ok) {
    throw new ApiError(resp.status, body.code ?? 'error', body.msg ?? resp.statusText)
  }
  return body as T
}

export function errorText(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.code) {
      case 'bad_request':
        return '查询条件不合法(时间区间最长92天)'
      case 'not_found':
        return '记录不存在'
      case 'unauthorized':
        return '登录已失效'
      default:
        return `请求失败：${e.code}`
    }
  }
  return e instanceof Error ? e.message : String(e)
}
