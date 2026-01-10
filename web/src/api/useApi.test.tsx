import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { useApi } from './useApi'
import { ApiContext } from './context'
import type { ApiClient } from './client'

describe('useApi', () => {
  it('should throw error when used outside ApiProvider', () => {
    // Suppress console.error for this test
    const originalError = console.error
    console.error = () => {}

    expect(() => {
      renderHook(() => useApi())
    }).toThrow('useApi must be used within ApiProvider')

    console.error = originalError
  })

  it('should return api client when used within ApiProvider', () => {
    const mockApiClient = {
      setToken: () => {},
      clearToken: () => {},
      providers: {},
      groups: {},
      config: {},
      status: {},
      logs: {},
    } as unknown as ApiClient

    const wrapper = ({ children }: { children: ReactNode }) => (
      <ApiContext.Provider value={mockApiClient}>
        {children}
      </ApiContext.Provider>
    )

    const { result } = renderHook(() => useApi(), { wrapper })

    expect(result.current).toBe(mockApiClient)
  })
})
