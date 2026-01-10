import { API_BASE, STORAGE_KEYS } from '../config'
import {
  type ApiClientDeps,
  type Storage,
  browserStorage,
  browserHttpClient,
} from './interfaces'

// API Error type
export class ApiError extends Error {
  code: string
  status: number

  constructor(code: string, message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
  }
}

// Token management factory
export function createTokenManager(storage: Storage) {
  return {
    get: (): string | null => storage.getItem(STORAGE_KEYS.AUTH_TOKEN),
    set: (token: string): void => storage.setItem(STORAGE_KEYS.AUTH_TOKEN, token),
    clear: (): void => storage.removeItem(STORAGE_KEYS.AUTH_TOKEN),
  }
}

// API Client factory with dependency injection
export function createApiClient(deps: ApiClientDeps) {
  const { storage, httpClient, baseUrl } = deps
  const tokenManager = createTokenManager(storage)

  async function request<T>(
    endpoint: string,
    options: RequestInit = {}
  ): Promise<T> {
    const token = tokenManager.get()

    const headers: HeadersInit = {
      'Content-Type': 'application/json',
      ...options.headers,
    }

    if (token) {
      (headers as Record<string, string>)['Authorization'] = `Bearer ${token}`
    }

    const response = await httpClient.fetch(`${baseUrl}${endpoint}`, {
      ...options,
      headers,
    })

    if (!response.ok) {
      const data = await response.json().catch(() => ({}))
      throw new ApiError(
        data.code || 'UNKNOWN_ERROR',
        data.message || response.statusText,
        response.status
      )
    }

    // 204 No Content
    if (response.status === 204) {
      return undefined as T
    }

    return response.json()
  }

  return {
    // Token management
    setToken: tokenManager.set,
    clearToken: tokenManager.clear,

    // Providers
    providers: {
      list: () => request<Provider[]>('/providers'),
      get: (id: string) => request<Provider>(`/providers/${id}`),
      create: (data: ProviderInput) =>
        request<Provider>('/providers', {
          method: 'POST',
          body: JSON.stringify(data),
        }),
      update: (id: string, data: ProviderInput) =>
        request<Provider>(`/providers/${id}`, {
          method: 'PUT',
          body: JSON.stringify(data),
        }),
      delete: (id: string) =>
        request<void>(`/providers/${id}`, {
          method: 'DELETE',
        }),
      enable: (id: string) =>
        request<void>(`/providers/${id}/enable`, {
          method: 'POST',
        }),
      disable: (id: string) =>
        request<void>(`/providers/${id}/disable`, {
          method: 'POST',
        }),
      reset: (id: string) =>
        request<void>(`/providers/${id}/reset`, {
          method: 'POST',
        }),
    },

    // Groups
    groups: {
      list: () => request<Group[]>('/groups'),
      get: (id: string) => request<Group>(`/groups/${id}`),
      create: (data: GroupInput) =>
        request<Group>('/groups', {
          method: 'POST',
          body: JSON.stringify(data),
        }),
      update: (id: string, data: GroupInput) =>
        request<Group>(`/groups/${id}`, {
          method: 'PUT',
          body: JSON.stringify(data),
        }),
      delete: (id: string) =>
        request<void>(`/groups/${id}`, {
          method: 'DELETE',
        }),
    },

    // Config
    config: {
      get: () => request<Record<string, string>>('/config'),
      update: (data: Record<string, string>) =>
        request<void>('/config', {
          method: 'PUT',
          body: JSON.stringify(data),
        }),
    },

    // Status
    status: {
      get: () => request<SystemStatus>('/status'),
      health: () => request<HealthState[]>('/health'),
    },

    // Logs
    logs: {
      list: (params?: { limit?: number; offset?: number }) => {
        const query = new URLSearchParams()
        if (params?.limit) query.set('limit', String(params.limit))
        if (params?.offset) query.set('offset', String(params.offset))
        const queryStr = query.toString()
        return request<RequestLog[]>(`/logs${queryStr ? `?${queryStr}` : ''}`)
      },
    },
  }
}

// Type for the API client instance
export type ApiClient = ReturnType<typeof createApiClient>

// Default instance for convenience (browser environment)
const defaultDeps: ApiClientDeps = {
  storage: browserStorage,
  httpClient: browserHttpClient,
  baseUrl: API_BASE,
}

export const api = createApiClient(defaultDeps)
export const setToken = api.setToken
export const clearToken = api.clearToken

// Type definitions
export interface Provider {
  id: string
  name: string
  base_url: string
  api_key: string
  api_types: string[]
  auth_mode: string
  group_id: string | null
  weight: number
  priority: number
  concurrency: number
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface ProviderInput {
  id?: string
  name: string
  base_url: string
  api_key: string
  api_types: string[]
  auth_mode?: string
  group_id?: string | null
  weight?: number
  priority?: number
  concurrency?: number
  enabled?: boolean
}

export interface Group {
  id: string
  name: string
  strategy: string
  priority: number
  weight: number
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface GroupInput {
  id?: string
  name: string
  strategy?: string
  priority?: number
  weight?: number
  enabled?: boolean
}

export interface HealthState {
  provider_id: string
  available: boolean
  success_count: number
  fail_count: number
  last_success: string | null
  last_failure: string | null
  last_error: string | null
  disabled_until: string | null
  disabled_reason: string | null
}

export interface SystemStatus {
  providers_total: number
  providers_healthy: number
  providers_unhealthy: number
  requests_today: number
}

export interface RequestLog {
  id: number
  provider_id: string
  api_type: string
  model: string
  client_ip: string
  user_id: string
  status_code: number
  latency_ms: number
  success: boolean
  error_msg: string | null
  created_at: string
}
