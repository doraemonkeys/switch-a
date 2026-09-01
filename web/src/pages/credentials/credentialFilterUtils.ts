import type { CredentialSession } from "../../api";
import type {
  CredentialKindFilter,
  CredentialSortOption,
  CredentialStatusFilter,
  CredentialUsageFilter,
} from "./CredentialFilterToolbar";

export interface CredentialFilterOptions {
  search: string;
  kindFilter: CredentialKindFilter;
  statusFilter: CredentialStatusFilter;
  usageFilter: CredentialUsageFilter;
  sortOption: CredentialSortOption;
}

export function filterAndSortCredentialSessions(
  sessions: CredentialSession[],
  options: CredentialFilterOptions,
): CredentialSession[] {
  const { search, kindFilter, statusFilter, usageFilter, sortOption } = options;

  return sessions
    .filter((session) => {
      if (kindFilter !== "all" && session.kind !== kindFilter) {
        return false;
      }
      if (
        statusFilter !== "all" &&
        session.auth_state.status !== statusFilter
      ) {
        return false;
      }
      if (usageFilter === "in_use" && session.route_references.length === 0) {
        return false;
      }
      if (usageFilter === "unused" && session.route_references.length > 0) {
        return false;
      }
      if (search.trim()) {
        const query = search.toLowerCase().trim();
        const matchesName = session.name.toLowerCase().includes(query);
        const matchesId = session.id.toLowerCase().includes(query);
        const matchesEmail = session.auth_state.email
          ?.toLowerCase()
          .includes(query);
        const matchesRoutes = session.route_references.some(
          (ref) =>
            ref.provider_name.toLowerCase().includes(query) ||
            ref.api_type.toLowerCase().includes(query),
        );
        if (!matchesName && !matchesId && !matchesEmail && !matchesRoutes) {
          return false;
        }
      }
      return true;
    })
    .sort((a, b) => {
      if (sortOption === "name") {
        return a.name.localeCompare(b.name);
      }
      if (sortOption === "routes") {
        return b.route_references.length - a.route_references.length;
      }
      return (
        new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
      );
    });
}
