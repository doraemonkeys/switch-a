import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import type { ReactNode } from 'react'
import { useGroups, useGroup } from './useGroups'
import { ApiContext } from '../api/context'
import type { ApiClient, Group } from '../api/client'

const mockGroup: Group = {
  id: '1',
  name: 'Primary',
  strategy: 'priority',
  priority: 1,
  weight: 1,
  enabled: true,
  created_at: '2024-01-01T00:00:00Z',
  updated_at: '2024-01-01T00:00:00Z',
}

function createMockApiClient() {
  return {
    setToken: vi.fn(),
    clearToken: vi.fn(),
    providers: { list: vi.fn(), get: vi.fn(), create: vi.fn(), update: vi.fn(), delete: vi.fn(), enable: vi.fn(), disable: vi.fn(), reset: vi.fn() },
    groups: {
      list: vi.fn().mockResolvedValue([mockGroup]),
      get: vi.fn().mockResolvedValue(mockGroup),
      create: vi.fn().mockResolvedValue(mockGroup),
      update: vi.fn().mockResolvedValue(mockGroup),
      delete: vi.fn().mockResolvedValue(undefined),
    },
    config: { get: vi.fn(), update: vi.fn() },
    status: { get: vi.fn(), health: vi.fn() },
    logs: { list: vi.fn() },
  } as unknown as ApiClient
}

function createWrapper(apiClient: ApiClient) {
  return ({ children }: { children: ReactNode }) => (
    <ApiContext.Provider value={apiClient}>{children}</ApiContext.Provider>
  )
}

describe('useGroups', () => {
  let mockApi: ReturnType<typeof createMockApiClient>

  beforeEach(() => {
    mockApi = createMockApiClient()
  })

  it('should fetch groups on mount', async () => {
    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    })

    expect(result.current.loading).toBe(true)

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.groups).toEqual([mockGroup])
    expect(result.current.error).toBeNull()
    expect(mockApi.groups.list).toHaveBeenCalled()
  })

  it('should handle fetch error', async () => {
    mockApi.groups.list = vi.fn().mockRejectedValue(new Error('Network error'))

    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.error).toBeInstanceOf(Error)
    expect(result.current.error?.message).toBe('Network error')
    expect(result.current.groups).toEqual([])
  })

  it('should handle non-Error rejection', async () => {
    mockApi.groups.list = vi.fn().mockRejectedValue('string error')

    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.error?.message).toBe('Failed to fetch groups')
  })

  it('should refetch groups', async () => {
    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    await act(async () => {
      await result.current.refetch()
    })

    expect(mockApi.groups.list).toHaveBeenCalledTimes(2)
  })

  it('should create group and refetch', async () => {
    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    const input = { name: 'New Group' }
    await act(async () => {
      await result.current.createGroup(input)
    })

    expect(mockApi.groups.create).toHaveBeenCalledWith(input)
    expect(mockApi.groups.list).toHaveBeenCalledTimes(2)
  })

  it('should update group and refetch', async () => {
    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    const input = { name: 'Updated Group' }
    await act(async () => {
      await result.current.updateGroup('1', input)
    })

    expect(mockApi.groups.update).toHaveBeenCalledWith('1', input)
    expect(mockApi.groups.list).toHaveBeenCalledTimes(2)
  })

  it('should delete group and refetch', async () => {
    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    await act(async () => {
      await result.current.deleteGroup('1')
    })

    expect(mockApi.groups.delete).toHaveBeenCalledWith('1')
    expect(mockApi.groups.list).toHaveBeenCalledTimes(2)
  })
})

describe('useGroup', () => {
  let mockApi: ReturnType<typeof createMockApiClient>

  beforeEach(() => {
    mockApi = createMockApiClient()
  })

  it('should fetch single group on mount', async () => {
    const { result } = renderHook(() => useGroup('1'), {
      wrapper: createWrapper(mockApi),
    })

    expect(result.current.loading).toBe(true)

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.group).toEqual(mockGroup)
    expect(result.current.error).toBeNull()
    expect(mockApi.groups.get).toHaveBeenCalledWith('1')
  })

  it('should handle fetch error', async () => {
    mockApi.groups.get = vi.fn().mockRejectedValue(new Error('Not found'))

    const { result } = renderHook(() => useGroup('1'), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.error).toBeInstanceOf(Error)
    expect(result.current.error?.message).toBe('Not found')
  })

  it('should handle non-Error rejection', async () => {
    mockApi.groups.get = vi.fn().mockRejectedValue('string error')

    const { result } = renderHook(() => useGroup('1'), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.error?.message).toBe('Failed to fetch group')
  })

  it('should not fetch when id is empty', async () => {
    const { result } = renderHook(() => useGroup(''), {
      wrapper: createWrapper(mockApi),
    })

    await new Promise(resolve => setTimeout(resolve, 50))

    expect(mockApi.groups.get).not.toHaveBeenCalled()
    expect(result.current.group).toBeNull()
  })

  it('should refetch group', async () => {
    const { result } = renderHook(() => useGroup('1'), {
      wrapper: createWrapper(mockApi),
    })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    await act(async () => {
      await result.current.refetch()
    })

    expect(mockApi.groups.get).toHaveBeenCalledTimes(2)
  })
})
