import { LOAD_MORE_INCREMENT, GROUP_LABEL_MAX_WIDTH } from "./constants";
import type {
  RequestGroup,
  SortField,
  SortOrder,
  GroupViewMode,
} from "./types";
import { formatDuration, sortRequests } from "./utils";
import { RequestRow } from "./RequestRow";

// =============================================================================
// Grouped Request List
// =============================================================================

export interface GroupedRequestListProps {
  groups: RequestGroup[];
  expandedGroups: Set<string>;
  toggleGroup: (key: string) => void;
  currentTime: number;
  longRunningThreshold: number;
  providerNames?: Map<string, string>;
  sortField: SortField;
  sortOrder: SortOrder;
  getVisibleCount: (key: string) => number;
  loadMore: (key: string, total: number) => void;
  showAll: (key: string, total: number) => void;
  /** Current grouping mode to determine which fields to show */
  groupBy: GroupViewMode;
}

export function GroupedRequestList({
  groups,
  expandedGroups,
  toggleGroup,
  currentTime,
  longRunningThreshold,
  providerNames,
  sortField,
  sortOrder,
  getVisibleCount,
  loadMore,
  showAll,
  groupBy,
}: GroupedRequestListProps) {
  if (groups.length === 0) {
    return (
      <div className="text-center py-8 text-text-secondary">
        No requests match your search
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {groups.map((group) => {
        const isExpanded = expandedGroups.has(group.key);
        const visibleCount = getVisibleCount(group.key);
        const sortedRequests = sortRequests(
          group.requests,
          sortField,
          sortOrder,
          currentTime,
        );
        const visibleRequests = sortedRequests.slice(0, visibleCount);
        const hasMore = group.requests.length > visibleCount;

        return (
          <div
            key={group.key}
            className={`border rounded-lg overflow-hidden transition-all ${
              group.hasLongRunning
                ? "border-amber-300 bg-amber-50/30 dark:border-amber-700/50 dark:bg-amber-900/10"
                : "border-border"
            }`}
          >
            {/* Group Header */}
            <button
              onClick={() => toggleGroup(group.key)}
              className="w-full flex items-center justify-between p-3 hover:bg-bg-hover transition-colors text-left"
              aria-expanded={isExpanded}
              aria-label={`${group.label} - ${group.requests.length} request${group.requests.length !== 1 ? "s" : ""}`}
            >
              <div className="flex items-center gap-3">
                {/* Expand/Collapse Icon */}
                <svg
                  className={`h-4 w-4 text-text-muted transition-transform ${
                    isExpanded ? "rotate-90" : ""
                  }`}
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                  aria-hidden="true"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M9 5l7 7-7 7"
                  />
                </svg>

                {/* Group Label */}
                <span
                  className="font-medium text-text-primary truncate"
                  style={{ maxWidth: GROUP_LABEL_MAX_WIDTH }}
                >
                  {group.label}
                </span>

                {/* Warning badge for long-running */}
                {group.hasLongRunning && (
                  <span className="text-amber-500 text-sm">⚠</span>
                )}
              </div>

              {/* Right side stats */}
              <div className="flex items-center gap-4 text-sm">
                <span className="text-text-secondary">
                  {group.requests.length} request
                  {group.requests.length !== 1 ? "s" : ""}
                </span>
                <span className="text-text-muted">
                  Longest:{" "}
                  <span
                    className={`font-mono ${
                      group.hasLongRunning
                        ? "text-amber-600 dark:text-amber-400"
                        : "text-text-primary"
                    }`}
                  >
                    {formatDuration(group.longestDuration)}
                  </span>
                </span>
              </div>
            </button>

            {/* Expanded Content */}
            {isExpanded && (
              <div className="border-t border-border-light">
                {/* Requests */}
                <ul className="divide-y divide-border-light list-none m-0 p-0">
                  {visibleRequests.map((req) => (
                    <li key={req.request_id}>
                      <RequestRow
                        request={req}
                        currentTime={currentTime}
                        longRunningThreshold={longRunningThreshold}
                        providerName={providerNames?.get(req.provider_id)}
                        compact
                        groupBy={groupBy}
                      />
                    </li>
                  ))}
                </ul>

                {/* Load More */}
                {hasMore && (
                  <div className="flex items-center justify-center gap-4 p-3 bg-bg-tertiary text-sm">
                    <span className="text-text-muted">
                      Showing {visibleCount} of {group.requests.length}
                    </span>
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        loadMore(group.key, group.requests.length);
                      }}
                      className="text-primary hover:underline font-medium"
                      aria-label={`Load ${Math.min(LOAD_MORE_INCREMENT, group.requests.length - visibleCount)} more requests in ${group.label}`}
                    >
                      Load{" "}
                      {Math.min(
                        LOAD_MORE_INCREMENT,
                        group.requests.length - visibleCount,
                      )}{" "}
                      more
                    </button>
                    <span className="text-text-muted">or</span>
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        showAll(group.key, group.requests.length);
                      }}
                      className="text-primary hover:underline font-medium"
                      aria-label={`Show all ${group.requests.length} requests in ${group.label}`}
                    >
                      Show all {group.requests.length}
                    </button>
                  </div>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
