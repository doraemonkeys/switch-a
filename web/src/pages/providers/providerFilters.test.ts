import { describe, expect, it } from "vitest";
import {
  filterProviders,
  readProviderListFilters,
  writeProviderListFilter,
} from "./providerFilters";

const providers = [
  {
    id: "healthy-provider",
    name: "Healthy Provider",
    group_id: "primary",
    enabled: true,
    api_types: [{ base_url: "https://healthy.example.com" }],
    health: { available: true, disabled_until: null },
  },
  {
    id: "circuit-open-provider",
    name: "Circuit Open Provider",
    group_id: "primary",
    enabled: true,
    api_types: [{ base_url: "https://open.example.com" }],
    health: { available: false, disabled_until: "2999-01-01T00:00:00Z" },
  },
  {
    id: "disabled-provider",
    name: "Disabled Provider",
    group_id: "secondary",
    enabled: false,
    api_types: [{ base_url: "https://disabled.example.com" }],
    health: null,
  },
] as const;

describe("provider list filters", () => {
  it("treats enabled as configuration state rather than health state", () => {
    const visibleProviders = filterProviders(providers, {
      searchQuery: "",
      groupId: "",
      visibility: "enabled",
    });

    expect(visibleProviders.map((provider) => provider.id)).toEqual([
      "healthy-provider",
      "circuit-open-provider",
    ]);
  });

  it("combines normalized search, group, and runtime status filters", () => {
    const visibleProviders = filterProviders(providers, {
      searchQuery: "  OPEN.EXAMPLE  ",
      groupId: "primary",
      visibility: "unhealthy",
    });

    expect(visibleProviders.map((provider) => provider.id)).toEqual([
      "circuit-open-provider",
    ]);
  });

  it("reads valid filters from the URL and rejects unknown visibility values", () => {
    expect(
      readProviderListFilters(
        new URLSearchParams("search=gpt&group=primary&status=enabled"),
      ),
    ).toEqual({
      searchQuery: "gpt",
      groupId: "primary",
      visibility: "enabled",
    });
    expect(
      readProviderListFilters(new URLSearchParams("status=unexpected"))
        .visibility,
    ).toBe("");
  });

  it("updates one filter without discarding unrelated URL state", () => {
    const current = new URLSearchParams("tab=usage&status=healthy");

    expect(
      writeProviderListFilter(current, "visibility", "enabled").toString(),
    ).toBe("tab=usage&status=enabled");
    expect(writeProviderListFilter(current, "visibility", "").toString()).toBe(
      "tab=usage",
    );
  });
});
