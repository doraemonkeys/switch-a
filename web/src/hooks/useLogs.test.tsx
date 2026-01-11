import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import type { ReactNode } from 'react'
import { useLogs } from './useLogs'
import { ApiContext } from '../api/context'
import type { ApiClient, RequestLog, LogsResponse } from '../api/client'

const mockLogs: RequestLog[] = [
  {
    id: 1,
    provider_id: '1',
    api_type: 'claude',
    model: 'claude-3',
    client_ip: '127.0.0.1',
    user_id: 'user1',
    status_code: 200,
    latency_ms: 150,
    success: true,
    error_msg: null,
    created_at: '2024-01-01T00:00:00Z',
  },
]

const mockLogsResponse: LogsResponse = {
  logs: mockLogs,
  total: 100,
  limit: 20,
  offset: 0,
}

function createMockApiClient() {
  return {
    setToken: vi.fn(),
    clearToken: vi.fn(),
    providers: { list: vi.fn(), get: vi.fn(), create: vi.fn(), update: vi.fn(), delete: vi.fn(), enable: vi.fn(), disable: vi.fn(), reset: vi.fn() },
    groups: { list: vi.fn(), get: vi.fn(), create: vi.fn(), update: vi.fn(), delete: vi.fn() },
    config: { get: vi.fn(), update: vi.fn() },
    status: { get: vi.fn(), health: vi.fn() },
    logs: {
      list: vi.fn().mockResolvedValue(mockLogsResponse),
    },
  } as unknown as ApiClient
}

function createWrapper(apiClient: ApiClient) {
  return ({ children }: { children: ReactNode }) => (
    <ApiContext.Provider value={apiClient}>{children}</ApiContext.Provider>
  )
}

describe('useLogs', () => {
  let mockApi: ReturnType<typeof createMockApiClient>

  beforeEach(() => {
    mockApi = createMockApiClient()
  })

  it('should fetch logs on mount with default params', async () => {
    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    })

    expect(result.current.loading).toBe(true)

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.logs).toEqual(mockLogs)
    expect(result.current.total).toBe(100)
    expect(result.current.error).toBeNull()
    expect(mockApi.logs.list).toHaveBeenCalledWith({ limit: 20, offset: 0 })
  })

  it('should fetch logs with custom initial params', async () => {
    const { result } = renderHook(() => useLogs({ limit: 50, offset: 100 }), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(mockApi.logs.list).toHaveBeenCalledWith({ limit: 50, offset: 100 })
    expect(result.current.params).toEqual({ limit: 50, offset: 100 })
  })

  it('should handle fetch error', async () => {
    mockApi.logs.list = vi.fn().mockRejectedValue(new Error('Network error'))

    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.error).toBeInstanceOf(Error)
    expect(result.current.error?.message).toBe('Network error')
    expect(result.current.logs).toEqual([])
  })

  it('should handle non-Error rejection', async () => {
    mockApi.logs.list = vi.fn().mockRejectedValue('string error')

    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.error?.message).toBe('Failed to fetch logs')
  })

  it('should refetch logs', async () => {
    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    await act(async () => {
      await result.current.refetch()
    })

    expect(mockApi.logs.list).toHaveBeenCalledTimes(2)
  })

  it('should update params and refetch', async () => {
    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    act(() => {
      result.current.setParams({ limit: 50, offset: 20 })
    })

    await waitFor(() => {
      expect(mockApi.logs.list).toHaveBeenCalledWith({ limit: 50, offset: 20 })
    })

    expect(result.current.params).toEqual({ limit: 50, offset: 20 })
  })
})
