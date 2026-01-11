import { useState, useEffect, useCallback } from 'react'
import { useApi } from '../api'
import type { RequestLog } from '../api/client'

interface UseLogsParams {
  limit?: number
  offset?: number
}

interface UseLogsResult {
  logs: RequestLog[]
  total: number
  loading: boolean
  error: Error | null
  refetch: () => Promise<void>
  setParams: (params: UseLogsParams) => void
  params: UseLogsParams
}

const DEFAULT_LIMIT = 20

export function useLogs(initialParams?: UseLogsParams): UseLogsResult {
  const api = useApi()
  const [logs, setLogs] = useState<RequestLog[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<Error | null>(null)
  const [params, setParams] = useState<UseLogsParams>({
    limit: initialParams?.limit ?? DEFAULT_LIMIT,
    offset: initialParams?.offset ?? 0,
  })

  const refetch = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const response = await api.logs.list(params)
      setLogs(response.logs)
      setTotal(response.total)
    } catch (err) {
      setError(err instanceof Error ? err : new Error('Failed to fetch logs'))
    } finally {
      setLoading(false)
    }
  }, [api, params])

  useEffect(() => {
    refetch()
  }, [refetch])

  return {
    logs,
    total,
    loading,
    error,
    refetch,
    setParams,
    params,
  }
}
