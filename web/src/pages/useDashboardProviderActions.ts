import { useState, useCallback } from "react";
import { useNavigate } from "react-router-dom";
import { useToast } from "../hooks/useToast";
import type { Provider, ProviderStatus } from "../api/client";

interface UseProviderActionsProps {
  providers: Provider[];
  enableProvider: (id: string) => Promise<void>;
  disableProvider: (id: string) => Promise<void>;
  resetProvider: (id: string) => Promise<void>;
  refetchStatus: () => void;
}

export function useDashboardProviderActions({
  providers,
  enableProvider,
  disableProvider,
  resetProvider,
  refetchStatus,
}: UseProviderActionsProps) {
  const navigate = useNavigate();
  const toast = useToast();

  const [detailProvider, setDetailProvider] = useState<Provider | null>(null);

  const handleProviderClick = useCallback(
    (providerStatus: ProviderStatus) => {
      const fullProvider = providers.find((p) => p.id === providerStatus.id);
      if (fullProvider) setDetailProvider(fullProvider);
    },
    [providers],
  );

  const handleCloseDetail = useCallback(() => setDetailProvider(null), []);

  const handleEditProvider = useCallback(
    (provider: Provider) => {
      setDetailProvider(null);
      navigate(`/providers?search=${provider.id}`);
    },
    [navigate],
  );

  const handleToggleProvider = useCallback(
    async (provider: Provider) => {
      try {
        if (provider.enabled) {
          await disableProvider(provider.id);
          toast.success(`Provider "${provider.name}" disabled`);
        } else {
          await enableProvider(provider.id);
          toast.success(`Provider "${provider.name}" enabled`);
        }
        refetchStatus();
        const updatedProvider = providers.find((p) => p.id === provider.id);
        if (updatedProvider) {
          setDetailProvider({ ...updatedProvider, enabled: !provider.enabled });
        }
      } catch (err) {
        toast.error(
          err instanceof Error ? err.message : "Failed to toggle provider",
        );
      }
    },
    [disableProvider, enableProvider, providers, refetchStatus, toast],
  );

  const handleResetProvider = useCallback(
    async (provider: Provider) => {
      try {
        await resetProvider(provider.id);
        toast.success(`Provider "${provider.name}" reset`);
        setDetailProvider(null);
        refetchStatus();
      } catch (err) {
        toast.error(
          err instanceof Error ? err.message : "Failed to reset provider",
        );
      }
    },
    [refetchStatus, resetProvider, toast],
  );

  return {
    navigate,
    detailProvider,
    handleProviderClick,
    handleCloseDetail,
    handleEditProvider,
    handleToggleProvider,
    handleResetProvider,
  };
}
