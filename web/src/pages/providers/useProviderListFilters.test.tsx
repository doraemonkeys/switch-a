import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { Link, MemoryRouter, Route, Routes, useLocation } from "react-router";
import type { ProviderVisibilityFilter } from "./types";
import {
  PROVIDER_LIST_FILTER_STORAGE_KEY,
  useProviderListFilters,
} from "./useProviderListFilters";

function ProviderFiltersHarness() {
  const { filters, setFilter } = useProviderListFilters();
  const location = useLocation();

  return (
    <>
      <select
        aria-label="Provider status"
        value={filters.visibility}
        onChange={(event) =>
          setFilter(
            "visibility",
            event.target.value as ProviderVisibilityFilter,
          )
        }
      >
        <option value="">All Providers</option>
        <option value="enabled">Enabled</option>
        <option value="unhealthy">Circuit Open</option>
      </select>
      <output data-testid="location-search">{location.search}</output>
      <Link to="/other">Other feature</Link>
    </>
  );
}

function OtherFeature() {
  return <Link to="/providers">Providers</Link>;
}

function renderNavigation(initialEntry = "/providers") {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <Routes>
        <Route path="/providers" element={<ProviderFiltersHarness />} />
        <Route path="/other" element={<OtherFeature />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("useProviderListFilters", () => {
  beforeEach(() => localStorage.clear());

  it("restores the selected filter after navigating to another feature", async () => {
    const user = userEvent.setup();
    renderNavigation();

    await user.selectOptions(
      screen.getByRole("combobox", { name: "Provider status" }),
      "enabled",
    );
    await user.click(screen.getByRole("link", { name: "Other feature" }));
    await user.click(screen.getByRole("link", { name: "Providers" }));

    expect(
      screen.getByRole("combobox", { name: "Provider status" }),
    ).toHaveValue("enabled");
    expect(screen.getByTestId("location-search")).toHaveTextContent(
      "?status=enabled",
    );
  });

  it("lets an explicit URL selection replace the previously persisted one", async () => {
    localStorage.setItem(
      PROVIDER_LIST_FILTER_STORAGE_KEY,
      JSON.stringify({
        searchQuery: "",
        groupId: "",
        visibility: "enabled",
      }),
    );
    const user = userEvent.setup();
    renderNavigation("/providers?status=unhealthy");

    expect(
      screen.getByRole("combobox", { name: "Provider status" }),
    ).toHaveValue("unhealthy");
    await user.click(screen.getByRole("link", { name: "Other feature" }));
    await user.click(screen.getByRole("link", { name: "Providers" }));

    expect(
      screen.getByRole("combobox", { name: "Provider status" }),
    ).toHaveValue("unhealthy");
  });
});
