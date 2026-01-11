import { useState, useEffect, useCallback } from 'react'
import { useApi } from '../api'

interface UseConfigResult {
    config: Record<string, string>
    loading: boolean
    error: Error | null
    saving: boolean
    refetch: () => Promise<void>
    updateConfig: (data: Record<string, string>) => Promise<void>
}

export function useConfig(): UseConfigResult {
    const api = useApi()
    const [config, setConfig] = useState<Record<string, string>>({})
    const [loading, setLoading] = useState(true)
    const [saving, setSaving] = useState(false)
    const [error, setError] = useState<Error | null>(null)

    const refetch = useCallback(async () => {
        setLoading(true)
        setError(null)
        try {
            const data = await api.config.get()
            setConfig(data)
        } catch (err) {
            setError(err instanceof Error ? err : new Error('Failed to fetch config'))
        } finally {
            setLoading(false)
        }
    }, [api])

    useEffect(() => {
        refetch()
    }, [refetch])

    const updateConfig = useCallback(async (data: Record<string, string>): Promise<void> => {
        setSaving(true)
        setError(null)
        try {
            await api.config.update(data)
            await refetch()
        } catch (err) {
            setError(err instanceof Error ? err : new Error('Failed to update config'))
            throw err
        } finally {
            setSaving(false)
        }
    }, [api, refetch])

    return {
        config,
        loading,
        error,
        saving,
        refetch,
        updateConfig,
    }
}
