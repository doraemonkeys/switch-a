import { useSearchParams } from "react-router";
import {
  readProviderListFilters,
  writeProviderListFilter,
} from "./providerFilters";
import type {
  ProviderListFilterField,
  ProviderListFilters,
} from "./providerFilters";

export function useProviderListFilters() {
  const [searchParams, setSearchParams] = useSearchParams();
  const filters = readProviderListFilters(searchParams);

  const setFilter = <K extends ProviderListFilterField>(
    field: K,
    value: ProviderListFilters[K],
  ) => {
    // Replacing history keeps typing in the search field from polluting Back,
    // while the URL remains the durable and shareable source of filter state.
    setSearchParams(
      (current) => writeProviderListFilter(current, field, value),
      { replace: true },
    );
  };

  return { filters, setFilter };
}
