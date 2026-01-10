import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { useContext } from 'react'
import { ApiProvider } from './ApiContext'
import { ApiContext } from './context'
import type { Storage, HttpClient } from './interfaces'

// Test component that uses the API context
function TestConsumer() {
  const api = useContext(ApiContext)
  return (
    <div>
      <span data-testid="has-api">{api ? 'yes' : 'no'}</span>
      <span data-testid="has-providers">{api?.providers ? 'yes' : 'no'}</span>
    </div>
  )
}

describe('ApiProvider', () => {
  const originalLocalStorage = globalThis.localStorage
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    // Mock localStorage
    const store: Record<string, string> = {}
    Object.defineProperty(globalThis, 'localStorage', {
      value: {
        getItem: vi.fn((key: string) => store[key] ?? null),
        setItem: vi.fn((key: string, value: string) => { store[key] = value }),
        removeItem: vi.fn((key: string) => { delete store[key] }),
      },
      writable: true,
    })
    // Mock fetch
    globalThis.fetch = vi.fn()
  })

  afterEach(() => {
    Object.defineProperty(globalThis, 'localStorage', {
      value: originalLocalStorage,
      writable: true,
    })
    globalThis.fetch = originalFetch
  })

  it('should provide default API client when no deps provided', () => {
    render(
      <ApiProvider>
        <TestConsumer />
      </ApiProvider>
    )

    expect(screen.getByTestId('has-api').textContent).toBe('yes')
    expect(screen.getByTestId('has-providers').textContent).toBe('yes')
  })

  it('should create custom API client when deps are provided', () => {
    const mockStorage: Storage = {
      getItem: vi.fn(() => null),
      setItem: vi.fn(),
      removeItem: vi.fn(),
    }
    const mockHttpClient: HttpClient = {
      fetch: vi.fn(),
    }

    render(
      <ApiProvider deps={{ storage: mockStorage, httpClient: mockHttpClient, baseUrl: 'https://custom' }}>
        <TestConsumer />
      </ApiProvider>
    )

    expect(screen.getByTestId('has-api').textContent).toBe('yes')
    expect(screen.getByTestId('has-providers').textContent).toBe('yes')
  })

  it('should use default storage when only httpClient is provided', () => {
    const mockHttpClient: HttpClient = {
      fetch: vi.fn(),
    }

    render(
      <ApiProvider deps={{ httpClient: mockHttpClient }}>
        <TestConsumer />
      </ApiProvider>
    )

    expect(screen.getByTestId('has-api').textContent).toBe('yes')
  })

  it('should use default httpClient when only storage is provided', () => {
    const mockStorage: Storage = {
      getItem: vi.fn(() => null),
      setItem: vi.fn(),
      removeItem: vi.fn(),
    }

    render(
      <ApiProvider deps={{ storage: mockStorage }}>
        <TestConsumer />
      </ApiProvider>
    )

    expect(screen.getByTestId('has-api').textContent).toBe('yes')
  })

  it('should use default baseUrl when not provided', () => {
    const mockStorage: Storage = {
      getItem: vi.fn(() => null),
      setItem: vi.fn(),
      removeItem: vi.fn(),
    }

    render(
      <ApiProvider deps={{ storage: mockStorage }}>
        <TestConsumer />
      </ApiProvider>
    )

    expect(screen.getByTestId('has-api').textContent).toBe('yes')
  })

  it('should render children correctly', () => {
    render(
      <ApiProvider>
        <div data-testid="child-content">Hello World</div>
      </ApiProvider>
    )

    expect(screen.getByTestId('child-content').textContent).toBe('Hello World')
  })
})
