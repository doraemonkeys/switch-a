import type { HealthState, ProviderAPIType } from "../../api";
import { getProviderStatus, PROVIDER_VISIBILITY_FILTERS } from "./types";
import type { ProviderVisibilityFilter } from "./types";

const FILTER_QUERY_PARAMETERS = {
  searchQuery: "search",
  groupId: "group",
  visibility: "status",
} as const;

export interface ProviderListFilters {
  searchQuery: string;
  groupId: string;
  visibility: ProviderVisibilityFilter;
}

export const EMPTY_PROVIDER_LIST_FILTERS: ProviderListFilters = {
  searchQuery: "",
  groupId: "",
  visibility: "",
};

export type ProviderListFilterField = keyof ProviderListFilters;

interface FilterableProvider {
  id: string;
  name: string;
  group_id: string | null;
  enabled: boolean;
  api_types: readonly Pick<ProviderAPIType, "base_url">[];
  health?: Pick<HealthState, "available" | "disabled_until"> | null;
}

function isProviderVisibilityFilter(
  value: string,
): value is Exclude<ProviderVisibilityFilter, ""> {
  return PROVIDER_VISIBILITY_FILTERS.some((candidate) => candidate === value);
}

export function readProviderListFilters(
  searchParams: URLSearchParams,
): ProviderListFilters {
  const visibility = searchParams.get(FILTER_QUERY_PARAMETERS.visibility) ?? "";

  return {
    searchQuery: searchParams.get(FILTER_QUERY_PARAMETERS.searchQuery) ?? "",
    groupId: searchParams.get(FILTER_QUERY_PARAMETERS.groupId) ?? "",
    visibility: isProviderVisibilityFilter(visibility) ? visibility : "",
  };
}

export function normalizeProviderListFilters(
  value: unknown,
): ProviderListFilters {
  if (!value || typeof value !== "object") {
    return EMPTY_PROVIDER_LIST_FILTERS;
  }

  const candidate = value as Record<string, unknown>;
  const visibility = candidate.visibility;

  return {
    searchQuery:
      typeof candidate.searchQuery === "string" ? candidate.searchQuery : "",
    groupId: typeof candidate.groupId === "string" ? candidate.groupId : "",
    visibility:
      typeof visibility === "string" && isProviderVisibilityFilter(visibility)
        ? visibility
        : "",
  };
}

export function hasProviderListFilterQuery(
  searchParams: URLSearchParams,
): boolean {
  return Object.values(FILTER_QUERY_PARAMETERS).some((parameter) =>
    searchParams.has(parameter),
  );
}

export function writeProviderListFilters(
  searchParams: URLSearchParams,
  filters: ProviderListFilters,
): URLSearchParams {
  const nextSearchParams = new URLSearchParams(searchParams);

  for (const field of Object.keys(
    FILTER_QUERY_PARAMETERS,
  ) as ProviderListFilterField[]) {
    const parameter = FILTER_QUERY_PARAMETERS[field];
    const value = filters[field];
    if (value === "") {
      nextSearchParams.delete(parameter);
    } else {
      nextSearchParams.set(parameter, value);
    }
  }

  return nextSearchParams;
}

export function providerListFiltersEqual(
  left: ProviderListFilters,
  right: ProviderListFilters,
): boolean {
  return (
    left.searchQuery === right.searchQuery &&
    left.groupId === right.groupId &&
    left.visibility === right.visibility
  );
}

export function hasProviderListFilters(filters: ProviderListFilters): boolean {
  return Boolean(
    filters.searchQuery.trim() || filters.groupId || filters.visibility,
  );
}

export function filterProviders<T extends FilterableProvider>(
  providers: readonly T[],
  filters: ProviderListFilters,
): T[] {
  const normalizedSearch = filters.searchQuery.trim().toLowerCase();

  return providers.filter((provider) => {
    if (normalizedSearch) {
      const matchesSearch =
        provider.name.toLowerCase().includes(normalizedSearch) ||
        provider.id.toLowerCase().includes(normalizedSearch) ||
        provider.api_types.some((apiType) =>
          apiType.base_url.toLowerCase().includes(normalizedSearch),
        );
      if (!matchesSearch) return false;
    }

    if (filters.groupId && provider.group_id !== filters.groupId) return false;

    if (filters.visibility === "enabled") return provider.enabled;
    if (!filters.visibility) return true;

    return (
      getProviderStatus(
        provider.enabled,
        provider.health?.available,
        provider.health?.disabled_until,
      ) === filters.visibility
    );
  });
}
