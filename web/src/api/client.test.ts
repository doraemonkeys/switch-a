import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ApiError, createTokenManager, createApiClient } from './client'
import type { Storage, HttpClient, ApiClientDeps } from './interfaces'

// Mock storage implementation
function createMockStorage(): Storage & { data: Map<string, string> } {
    const data = new Map<string, string>()
    return {
        data,
        getItem: vi.fn((key: string) => data.get(key) ?? null),
        setItem: vi.fn((key: string, value: string) => { data.set(key, value) }),
        removeItem: vi.fn((key: string) => { data.delete(key) }),
    }
}

// Mock HTTP client implementation
function createMockHttpClient(): HttpClient & { mockResponse: (response: Partial<Response>) => void } {
    const mockFetch = vi.fn()

    return {
        fetch: mockFetch,
        mockResponse: (response: Partial<Response>) => {
            mockFetch.mockResolvedValue({
                ok: response.ok ?? true,
                status: response.status ?? 200,
                statusText: response.statusText ?? 'OK',
                json: response.json ?? (() => Promise.resolve({})),
                ...response,
            })
        },
    }
}

describe('ApiError', () => {
    it('should create an error with code, message, and status', () => {
        const error = new ApiError('AUTH_FAILED', 'Authentication failed', 401)

        expect(error).toBeInstanceOf(Error)
        expect(error.name).toBe('ApiError')
        expect(error.code).toBe('AUTH_FAILED')
        expect(error.message).toBe('Authentication failed')
        expect(error.status).toBe(401)
    })
})

describe('createTokenManager', () => {
    let mockStorage: ReturnType<typeof createMockStorage>
    let tokenManager: ReturnType<typeof createTokenManager>

    beforeEach(() => {
        mockStorage = createMockStorage()
        tokenManager = createTokenManager(mockStorage)
    })

    it('should get token from storage', () => {
        mockStorage.data.set('admin_token', 'test-token')

        expect(tokenManager.get()).toBe('test-token')
        expect(mockStorage.getItem).toHaveBeenCalledWith('admin_token')
    })

    it('should return null when no token exists', () => {
        expect(tokenManager.get()).toBeNull()
    })

    it('should set token in storage', () => {
        tokenManager.set('new-token')

        expect(mockStorage.setItem).toHaveBeenCalledWith('admin_token', 'new-token')
        expect(mockStorage.data.get('admin_token')).toBe('new-token')
    })

    it('should clear token from storage', () => {
        mockStorage.data.set('admin_token', 'test-token')

        tokenManager.clear()

        expect(mockStorage.removeItem).toHaveBeenCalledWith('admin_token')
        expect(mockStorage.data.has('admin_token')).toBe(false)
    })
})

describe('createApiClient', () => {
    let mockStorage: ReturnType<typeof createMockStorage>
    let mockHttpClient: ReturnType<typeof createMockHttpClient>
    let deps: ApiClientDeps
    let api: ReturnType<typeof createApiClient>

    beforeEach(() => {
        mockStorage = createMockStorage()
        mockHttpClient = createMockHttpClient()
        deps = {
            storage: mockStorage,
            httpClient: mockHttpClient,
            baseUrl: 'https://test-api.example.com',
        }
        api = createApiClient(deps)
    })

    describe('token management', () => {
        it('should set and clear token', () => {
            api.setToken('test-token')
            expect(mockStorage.data.get('admin_token')).toBe('test-token')

            api.clearToken()
            expect(mockStorage.data.has('admin_token')).toBe(false)
        })
    })

    describe('request handling', () => {
        it('should make request without auth header when no token', async () => {
            mockHttpClient.mockResponse({
                ok: true,
                status: 200,
                json: () => Promise.resolve([{ id: '1', name: 'Test' }]),
            })

            await api.providers.list()

            expect(mockHttpClient.fetch).toHaveBeenCalledWith(
                'https://test-api.example.com/providers',
                expect.objectContaining({
                    headers: { 'Content-Type': 'application/json' },
                })
            )
        })

        it('should include auth header when token exists', async () => {
            mockStorage.data.set('admin_token', 'bearer-token')
            mockHttpClient.mockResponse({
                ok: true,
                status: 200,
                json: () => Promise.resolve([]),
            })

            await api.providers.list()

            expect(mockHttpClient.fetch).toHaveBeenCalledWith(
                'https://test-api.example.com/providers',
                expect.objectContaining({
                    headers: {
                        'Content-Type': 'application/json',
                        Authorization: 'Bearer bearer-token',
                    },
                })
            )
        })

        it('should handle 204 No Content response', async () => {
            mockHttpClient.mockResponse({
                ok: true,
                status: 204,
                json: () => Promise.reject(new Error('No content')),
            })

            const result = await api.providers.delete('123')

            expect(result).toBeUndefined()
        })

        it('should throw ApiError on non-ok response', async () => {
            mockHttpClient.mockResponse({
                ok: false,
                status: 401,
                statusText: 'Unauthorized',
                json: () => Promise.resolve({ code: 'AUTH_REQUIRED', message: 'Please login' }),
            })

            await expect(api.providers.list()).rejects.toThrow(ApiError)
            await expect(api.providers.list()).rejects.toMatchObject({
                code: 'AUTH_REQUIRED',
                message: 'Please login',
                status: 401,
            })
        })

        it('should handle JSON parse error in error response', async () => {
            mockHttpClient.mockResponse({
                ok: false,
                status: 500,
                statusText: 'Internal Server Error',
                json: () => Promise.reject(new Error('Invalid JSON')),
            })

            await expect(api.providers.list()).rejects.toMatchObject({
                code: 'UNKNOWN_ERROR',
                message: 'Internal Server Error',
                status: 500,
            })
        })
    })
})

describe('createApiClient providers API', () => {
    let mockStorage: ReturnType<typeof createMockStorage>
    let mockHttpClient: ReturnType<typeof createMockHttpClient>
    let api: ReturnType<typeof createApiClient>

    beforeEach(() => {
        mockStorage = createMockStorage()
        mockHttpClient = createMockHttpClient()
        api = createApiClient({
            storage: mockStorage,
            httpClient: mockHttpClient,
            baseUrl: 'https://test-api.example.com',
        })
    })

    it('should list providers', async () => {
        const providers = [{ id: '1', name: 'OpenAI' }]
        mockHttpClient.mockResponse({
            ok: true,
            status: 200,
            json: () => Promise.resolve(providers),
        })

        const result = await api.providers.list()

        expect(result).toEqual(providers)
        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/providers',
            expect.any(Object)
        )
    })

    it('should get provider by id', async () => {
        const provider = { id: '1', name: 'OpenAI' }
        mockHttpClient.mockResponse({
            ok: true,
            status: 200,
            json: () => Promise.resolve(provider),
        })

        const result = await api.providers.get('1')

        expect(result).toEqual(provider)
        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/providers/1',
            expect.any(Object)
        )
    })

    it('should create provider', async () => {
        const input = { name: 'Test', base_url: 'https://test.example.com', api_key: 'key', api_types: ['claude'] }
        const created = { id: '1', ...input }
        mockHttpClient.mockResponse({
            ok: true,
            status: 201,
            json: () => Promise.resolve(created),
        })

        const result = await api.providers.create(input)

        expect(result).toEqual(created)
        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/providers',
            expect.objectContaining({
                method: 'POST',
                body: JSON.stringify(input),
            })
        )
    })

    it('should update provider', async () => {
        const input = { name: 'Updated', base_url: 'https://test.example.com', api_key: 'key', api_types: ['claude'] }
        mockHttpClient.mockResponse({
            ok: true,
            status: 200,
            json: () => Promise.resolve({ id: '1', ...input }),
        })

        await api.providers.update('1', input)

        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/providers/1',
            expect.objectContaining({
                method: 'PUT',
                body: JSON.stringify(input),
            })
        )
    })

    it('should delete provider', async () => {
        mockHttpClient.mockResponse({ ok: true, status: 204 })

        await api.providers.delete('1')

        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/providers/1',
            expect.objectContaining({ method: 'DELETE' })
        )
    })

    it('should enable provider', async () => {
        mockHttpClient.mockResponse({ ok: true, status: 204 })

        await api.providers.enable('1')

        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/providers/1/enable',
            expect.objectContaining({ method: 'POST' })
        )
    })

    it('should disable provider', async () => {
        mockHttpClient.mockResponse({ ok: true, status: 204 })

        await api.providers.disable('1')

        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/providers/1/disable',
            expect.objectContaining({ method: 'POST' })
        )
    })

    it('should reset provider', async () => {
        mockHttpClient.mockResponse({ ok: true, status: 204 })

        await api.providers.reset('1')

        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/providers/1/reset',
            expect.objectContaining({ method: 'POST' })
        )
    })
})

describe('createApiClient groups API', () => {
    let mockStorage: ReturnType<typeof createMockStorage>
    let mockHttpClient: ReturnType<typeof createMockHttpClient>
    let api: ReturnType<typeof createApiClient>

    beforeEach(() => {
        mockStorage = createMockStorage()
        mockHttpClient = createMockHttpClient()
        api = createApiClient({
            storage: mockStorage,
            httpClient: mockHttpClient,
            baseUrl: 'https://test-api.example.com',
        })
    })

    it('should list groups', async () => {
        const groups = [{ id: '1', name: 'Primary' }]
        mockHttpClient.mockResponse({
            ok: true,
            status: 200,
            json: () => Promise.resolve(groups),
        })

        const result = await api.groups.list()

        expect(result).toEqual(groups)
    })

    it('should get group by id', async () => {
        mockHttpClient.mockResponse({
            ok: true,
            status: 200,
            json: () => Promise.resolve({ id: '1', name: 'Primary' }),
        })

        await api.groups.get('1')

        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/groups/1',
            expect.any(Object)
        )
    })

    it('should create group', async () => {
        const input = { name: 'New Group' }
        mockHttpClient.mockResponse({
            ok: true,
            status: 201,
            json: () => Promise.resolve({ id: '1', ...input }),
        })

        await api.groups.create(input)

        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/groups',
            expect.objectContaining({
                method: 'POST',
                body: JSON.stringify(input),
            })
        )
    })

    it('should update group', async () => {
        const input = { name: 'Updated Group' }
        mockHttpClient.mockResponse({
            ok: true,
            status: 200,
            json: () => Promise.resolve({ id: '1', ...input }),
        })

        await api.groups.update('1', input)

        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/groups/1',
            expect.objectContaining({
                method: 'PUT',
                body: JSON.stringify(input),
            })
        )
    })

    it('should delete group', async () => {
        mockHttpClient.mockResponse({ ok: true, status: 204 })

        await api.groups.delete('1')

        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/groups/1',
            expect.objectContaining({ method: 'DELETE' })
        )
    })
})

describe('createApiClient config API', () => {
    let mockStorage: ReturnType<typeof createMockStorage>
    let mockHttpClient: ReturnType<typeof createMockHttpClient>
    let api: ReturnType<typeof createApiClient>

    beforeEach(() => {
        mockStorage = createMockStorage()
        mockHttpClient = createMockHttpClient()
        api = createApiClient({
            storage: mockStorage,
            httpClient: mockHttpClient,
            baseUrl: 'https://test-api.example.com',
        })
    })

    it('should get config', async () => {
        const config = { sticky_ttl: '300' }
        mockHttpClient.mockResponse({
            ok: true,
            status: 200,
            json: () => Promise.resolve(config),
        })

        const result = await api.config.get()

        expect(result).toEqual(config)
        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/config',
            expect.any(Object)
        )
    })

    it('should update config', async () => {
        mockHttpClient.mockResponse({ ok: true, status: 204 })

        await api.config.update({ sticky_ttl: '600' })

        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/config',
            expect.objectContaining({
                method: 'PUT',
                body: JSON.stringify({ sticky_ttl: '600' }),
            })
        )
    })
})

describe('createApiClient status API', () => {
    let mockStorage: ReturnType<typeof createMockStorage>
    let mockHttpClient: ReturnType<typeof createMockHttpClient>
    let api: ReturnType<typeof createApiClient>

    beforeEach(() => {
        mockStorage = createMockStorage()
        mockHttpClient = createMockHttpClient()
        api = createApiClient({
            storage: mockStorage,
            httpClient: mockHttpClient,
            baseUrl: 'https://test-api.example.com',
        })
    })

    it('should get system status', async () => {
        const status = { providers: [{ id: '1', name: 'Provider 1', enabled: true, current_requests: 0, health: null }] }
        mockHttpClient.mockResponse({
            ok: true,
            status: 200,
            json: () => Promise.resolve(status),
        })

        const result = await api.status.get()

        expect(result).toEqual(status)
    })

    it('should get health states', async () => {
        const health = [{ provider_id: '1', available: true }]
        mockHttpClient.mockResponse({
            ok: true,
            status: 200,
            json: () => Promise.resolve(health),
        })

        const result = await api.status.health()

        expect(result).toEqual(health)
        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/health',
            expect.any(Object)
        )
    })
})

describe('createApiClient logs API', () => {
    let mockStorage: ReturnType<typeof createMockStorage>
    let mockHttpClient: ReturnType<typeof createMockHttpClient>
    let api: ReturnType<typeof createApiClient>

    beforeEach(() => {
        mockStorage = createMockStorage()
        mockHttpClient = createMockHttpClient()
        api = createApiClient({
            storage: mockStorage,
            httpClient: mockHttpClient,
            baseUrl: 'https://test-api.example.com',
        })
    })

    it('should list logs without params', async () => {
        const logsResponse = { logs: [{ id: 1, provider_id: '1' }], total: 1, limit: 20, offset: 0 }
        mockHttpClient.mockResponse({
            ok: true,
            status: 200,
            json: () => Promise.resolve(logsResponse),
        })

        const result = await api.logs.list()

        expect(result).toEqual(logsResponse)
        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/logs',
            expect.any(Object)
        )
    })

    it('should list logs with limit param', async () => {
        mockHttpClient.mockResponse({
            ok: true,
            status: 200,
            json: () => Promise.resolve([]),
        })

        await api.logs.list({ limit: 50 })

        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/logs?limit=50',
            expect.any(Object)
        )
    })

    it('should list logs with offset param', async () => {
        mockHttpClient.mockResponse({
            ok: true,
            status: 200,
            json: () => Promise.resolve([]),
        })

        await api.logs.list({ offset: 100 })

        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/logs?offset=100',
            expect.any(Object)
        )
    })

    it('should list logs with both limit and offset', async () => {
        mockHttpClient.mockResponse({
            ok: true,
            status: 200,
            json: () => Promise.resolve([]),
        })

        await api.logs.list({ limit: 50, offset: 100 })

        expect(mockHttpClient.fetch).toHaveBeenCalledWith(
            'https://test-api.example.com/logs?limit=50&offset=100',
            expect.any(Object)
        )
    })
})
