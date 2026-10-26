import { useEffect, useState } from 'react'
import { errorText } from './client'

export interface ApiState<T> {
  data: T | undefined
  loading: boolean
  error: string | undefined
  reload: () => void
}

// useApi 在deps变化时重新请求；组件卸载或deps变化后，旧请求的结果会被丢弃。
export function useApi<T>(fn: () => Promise<T>, deps: unknown[]): ApiState<T> {
  const [data, setData] = useState<T>()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string>()
  const [tick, setTick] = useState(0)

  useEffect(() => {
    let alive = true
    setLoading(true)
    setError(undefined)
    fn()
      .then((d) => alive && setData(d))
      .catch((e) => alive && setError(errorText(e)))
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick])

  return { data, loading, error, reload: () => setTick((t) => t + 1) }
}
