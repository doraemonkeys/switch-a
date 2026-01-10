import { type ReactNode } from 'react'
import { createApiClient, api as defaultApi } from './client'
import { type ApiClientDeps, browserStorage, browserHttpClient } from './interfaces'
import { API_BASE } from '../config'
import { ApiContext } from './context'

interface ApiProviderProps {
  children: ReactNode
  deps?: Partial<ApiClientDeps>
}

// Provider for dependency injection (useful for testing)
export function ApiProvider({ children, deps }: ApiProviderProps) {
  const apiClient = deps
    ? createApiClient({
      storage: deps.storage ?? browserStorage,
      httpClient: deps.httpClient ?? browserHttpClient,
      baseUrl: deps.baseUrl ?? API_BASE,
    })
    : defaultApi

  return <ApiContext.Provider value={apiClient}>{children}</ApiContext.Provider>
}
