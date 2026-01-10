import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { browserStorage, browserHttpClient } from './interfaces'

describe('browserStorage', () => {
    const originalLocalStorage = globalThis.localStorage

    beforeEach(() => {
        // Create a mock localStorage
        const store: Record<string, string> = {}
        const mockLocalStorage = {
            getItem: vi.fn((key: string) => store[key] ?? null),
            setItem: vi.fn((key: string, value: string) => { store[key] = value }),
            removeItem: vi.fn((key: string) => { delete store[key] }),
            clear: vi.fn(() => { Object.keys(store).forEach(k => delete store[k]) }),
            length: 0,
            key: vi.fn(),
        }
        Object.defineProperty(globalThis, 'localStorage', {
            value: mockLocalStorage,
            writable: true,
        })
    })

    afterEach(() => {
        Object.defineProperty(globalThis, 'localStorage', {
            value: originalLocalStorage,
            writable: true,
        })
    })

    it('should get item from localStorage', () => {
        localStorage.setItem('test-key', 'test-value')

        const result = browserStorage.getItem('test-key')

        expect(result).toBe('test-value')
    })

    it('should return null for non-existent key', () => {
        const result = browserStorage.getItem('non-existent')

        expect(result).toBeNull()
    })

    it('should set item in localStorage', () => {
        browserStorage.setItem('new-key', 'new-value')

        expect(localStorage.setItem).toHaveBeenCalledWith('new-key', 'new-value')
    })

    it('should remove item from localStorage', () => {
        browserStorage.removeItem('key-to-remove')

        expect(localStorage.removeItem).toHaveBeenCalledWith('key-to-remove')
    })
})

describe('browserHttpClient', () => {
    const originalFetch = globalThis.fetch

    beforeEach(() => {
        globalThis.fetch = vi.fn()
    })

    afterEach(() => {
        globalThis.fetch = originalFetch
    })

    it('should call global fetch with correct arguments', async () => {
        const mockResponse = new Response(JSON.stringify({ data: 'test' }), { status: 200 })
        vi.mocked(globalThis.fetch).mockResolvedValue(mockResponse)

        const result = await browserHttpClient.fetch('http://test.com/api', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
        })

        expect(globalThis.fetch).toHaveBeenCalledWith('http://test.com/api', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
        })
        expect(result).toBe(mockResponse)
    })

    it('should work with URL object', async () => {
        const mockResponse = new Response('', { status: 200 })
        vi.mocked(globalThis.fetch).mockResolvedValue(mockResponse)

        const url = new URL('http://test.com/api')
        await browserHttpClient.fetch(url)

        expect(globalThis.fetch).toHaveBeenCalledWith(url, undefined)
    })
})
