import { useEffect, useRef } from "react";
import { useSearchParams } from "react-router";
import { useLocalStorage } from "../../hooks/useLocalStorage";
import {
  EMPTY_PROVIDER_LIST_FILTERS,
  hasProviderListFilterQuery,
  hasProviderListFilters,
  normalizeProviderListFilters,
  providerListFiltersEqual,
  readProviderListFilters,
  writeProviderListFilters,
} from "./providerFilters";
import type {
  ProviderListFilterField,
  ProviderListFilters,
} from "./providerFilters";

export const PROVIDER_LIST_FILTER_STORAGE_KEY = "providers:listFilters";

export function useProviderListFilters() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [persistedValue, setPersistedValue] = useLocalStorage<unknown>(
    PROVIDER_LIST_FILTER_STORAGE_KEY,
    EMPTY_PROVIDER_LIST_FILTERS,
  );
  const urlFilters = readProviderListFilters(searchParams);
  const persistedFilters = normalizeProviderListFilters(persistedValue);
  const hasURLFilters = hasProviderListFilterQuery(searchParams);
  const filters = hasURLFilters ? urlFilters : persistedFilters;
  const persistedSearchQuery = persistedFilters.searchQuery;
  const persistedGroupId = persistedFilters.groupId;
  const persistedVisibility = persistedFilters.visibility;
  const urlSearchQuery = urlFilters.searchQuery;
  const urlGroupId = urlFilters.groupId;
  const urlVisibility = urlFilters.visibility;

  // A setFilter() write lands in two stores that commit at different times:
  // useLocalStorage dispatches a synchronous store event that React flushes
  // before the router navigation commits. On that intermediate render the URL
  // still holds the old filters while localStorage already holds the new ones,
  // so the mirror logic below would mistake the user's own write for an
  // outdated persisted value and overwrite it — and the rehydrate logic would
  // then put the old filter back into the URL. Skipping one effect pass after
  // an explicit user write lets both stores converge before any mirroring runs.
  const userWritePendingRef = useRef(false);

  useEffect(() => {
    if (userWritePendingRef.current) {
      userWritePendingRef.current = false;
      return;
    }
    const currentPersistedFilters: ProviderListFilters = {
      searchQuery: persistedSearchQuery,
      groupId: persistedGroupId,
      visibility: persistedVisibility,
    };
    const currentURLFilters: ProviderListFilters = {
      searchQuery: urlSearchQuery,
      groupId: urlGroupId,
      visibility: urlVisibility,
    };

    if (hasURLFilters) {
      if (
        !providerListFiltersEqual(currentURLFilters, currentPersistedFilters)
      ) {
        setPersistedValue(currentURLFilters);
      }
      return;
    }

    if (hasProviderListFilters(currentPersistedFilters)) {
      // Rehydrate the URL after navigation so the visible state remains
      // inspectable and shareable instead of becoming hidden browser state.
      setSearchParams(
        (current) => writeProviderListFilters(current, currentPersistedFilters),
        { replace: true },
      );
    }
  }, [
    hasURLFilters,
    persistedGroupId,
    persistedSearchQuery,
    persistedVisibility,
    setPersistedValue,
    setSearchParams,
    urlGroupId,
    urlSearchQuery,
    urlVisibility,
  ]);

  const setFilter = <K extends ProviderListFilterField>(
    field: K,
    value: ProviderListFilters[K],
  ) => {
    const nextFilters = { ...filters, [field]: value };

    userWritePendingRef.current = true;

    // Replacing history keeps typing in the search field from polluting Back,
    // while local storage carries the same selection across feature navigation.
    setSearchParams(
      (current) => writeProviderListFilters(current, nextFilters),
      { replace: true },
    );
    setPersistedValue(nextFilters);
  };

  return { filters, setFilter };
}
