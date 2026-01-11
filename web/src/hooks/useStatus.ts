import { useState, useEffect, useCallback, useMemo } from 'react'
import { useApi } from '../api'
import type { SystemStatus, SystemStatusSummary, HealthState } from '../api/client'

interface UseStatusResult {
    status: SystemStatus | null
    summary: SystemStatusSummary | null
    loading: boolean
    error: Error | null
    refetch: () => Promise<void>
}

export function useStatus(): UseStatusResult {
    const api = useApi()
    const [status, setStatus] = useState<SystemStatus | null>(null)
    const [loading, setLoading] = useState(true)
    const [error, setError] = useState<Error | null>(null)

    const refetch = useCallback(async () => {
        setLoading(true)
        setError(null)
        try {
            const data = await api.status.get()
            setStatus(data)
        } catch (err) {
            setError(err instanceof Error ? err : new Error('Failed to fetch status'))
        } finally {
            setLoading(false)
        }
    }, [api])

    useEffect(() => {
        refetch()
    }, [refetch])

    // Compute summary statistics from the raw status data
    const summary = useMemo((): SystemStatusSummary | null => {
        if (!status) return null

        const providers = status.providers
        const total = providers.length
        const healthy = providers.filter(
            (p) => p.enabled && p.health?.available !== false
        ).length
        const unhealthy = total - healthy

        return {
            providers_total: total,
            providers_healthy: healthy,
            providers_unhealthy: unhealthy,
            requests_today: 0, // This would need a separate API endpoint to track
        }
    }, [status])

    return { status, summary, loading, error, refetch }
}

interface UseHealthStatesResult {
    healthStates: HealthState[]
    loading: boolean
    error: Error | null
    refetch: () => Promise<void>
}

export function useHealthStates(): UseHealthStatesResult {
    const api = useApi()
    const [healthStates, setHealthStates] = useState<HealthState[]>([])
    const [loading, setLoading] = useState(true)
    const [error, setError] = useState<Error | null>(null)

    const refetch = useCallback(async () => {
        setLoading(true)
        setError(null)
        try {
            const data = await api.status.health()
            setHealthStates(data)
        } catch (err) {
            setError(err instanceof Error ? err : new Error('Failed to fetch health states'))
        } finally {
            setLoading(false)
        }
    }, [api])

    useEffect(() => {
        refetch()
    }, [refetch])

    return { healthStates, loading, error, refetch }
}
