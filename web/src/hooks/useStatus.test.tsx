import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import type { ReactNode } from 'react'
import { useStatus, useHealthStates } from './useStatus'
import { ApiContext } from '../api/context'
import type { ApiClient, SystemStatus, HealthState } from '../api/client'

const mockHealthStates: HealthState[] = [
  {
    provider_id: '1',
    available: true,
    success_count: 100,
    fail_count: 0,
    last_success: '2024-01-01T00:00:00Z',
    last_failure: null,
    last_error: null,
    disabled_until: null,
    disabled_reason: null,
  },
]

const mockStatus: SystemStatus = {
  providers: [
    {
      id: '1',
      name: 'Provider 1',
      enabled: true,
      current_requests: 5,
      health: mockHealthStates[0],
    },
    {
      id: '2',
      name: 'Provider 2',
      enabled: true,
      current_requests: 0,
      health: null,
    },
    {
      id: '3',
      name: 'Provider 3',
      enabled: false,
      current_requests: 0,
      health: null,
    },
  ],
}

function createMockApiClient() {
  return {
    setToken: vi.fn(),
    clearToken: vi.fn(),
    providers: { list: vi.fn(), get: vi.fn(), create: vi.fn(), update: vi.fn(), delete: vi.fn(), enable: vi.fn(), disable: vi.fn(), reset: vi.fn() },
    groups: { list: vi.fn(), get: vi.fn(), create: vi.fn(), update: vi.fn(), delete: vi.fn() },
    config: { get: vi.fn(), update: vi.fn() },
    status: {
      get: vi.fn().mockResolvedValue(mockStatus),
      health: vi.fn().mockResolvedValue(mockHealthStates),
    },
    logs: { list: vi.fn() },
  } as unknown as ApiClient
}

function createWrapper(apiClient: ApiClient) {
  return ({ children }: { children: ReactNode }) => (
    <ApiContext.Provider value={apiClient}>{children}</ApiContext.Provider>
  )
}

describe('useStatus', () => {
  let mockApi: ReturnType<typeof createMockApiClient>

  beforeEach(() => {
    mockApi = createMockApiClient()
  })

  it('should fetch status on mount', async () => {
    const { result } = renderHook(() => useStatus(), {
      wrapper: createWrapper(mockApi),
    })

    expect(result.current.loading).toBe(true)

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.status).toEqual(mockStatus)
    expect(result.current.summary).toEqual({
      providers_total: 3,
      providers_healthy: 2,
      providers_unhealthy: 1,
      requests_today: 0,
    })
    expect(result.current.error).toBeNull()
    expect(mockApi.status.get).toHaveBeenCalled()
  })

  it('should handle fetch error', async () => {
    mockApi.status.get = vi.fn().mockRejectedValue(new Error('Network error'))

    const { result } = renderHook(() => useStatus(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.error).toBeInstanceOf(Error)
    expect(result.current.error?.message).toBe('Network error')
    expect(result.current.status).toBeNull()
    expect(result.current.summary).toBeNull()
  })

  it('should handle non-Error rejection', async () => {
    mockApi.status.get = vi.fn().mockRejectedValue('string error')

    const { result } = renderHook(() => useStatus(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.error?.message).toBe('Failed to fetch status')
  })

  it('should refetch status', async () => {
    const { result } = renderHook(() => useStatus(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    await act(async () => {
      await result.current.refetch()
    })

    expect(mockApi.status.get).toHaveBeenCalledTimes(2)
  })
})

describe('useHealthStates', () => {
  let mockApi: ReturnType<typeof createMockApiClient>

  beforeEach(() => {
    mockApi = createMockApiClient()
  })

  it('should fetch health states on mount', async () => {
    const { result } = renderHook(() => useHealthStates(), {
      wrapper: createWrapper(mockApi),
    })

    expect(result.current.loading).toBe(true)

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.healthStates).toEqual(mockHealthStates)
    expect(result.current.error).toBeNull()
    expect(mockApi.status.health).toHaveBeenCalled()
  })

  it('should handle fetch error', async () => {
    mockApi.status.health = vi.fn().mockRejectedValue(new Error('Network error'))

    const { result } = renderHook(() => useHealthStates(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.error).toBeInstanceOf(Error)
    expect(result.current.error?.message).toBe('Network error')
    expect(result.current.healthStates).toEqual([])
  })

  it('should handle non-Error rejection', async () => {
    mockApi.status.health = vi.fn().mockRejectedValue('string error')

    const { result } = renderHook(() => useHealthStates(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.error?.message).toBe('Failed to fetch health states')
  })

  it('should refetch health states', async () => {
    const { result } = renderHook(() => useHealthStates(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    await act(async () => {
      await result.current.refetch()
    })

    expect(mockApi.status.health).toHaveBeenCalledTimes(2)
  })
})
