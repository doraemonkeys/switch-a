import { useState } from "react";
import type { ActiveRequest } from "../../api/types";
import {
  INITIAL_VISIBLE_COUNT,
  LOAD_MORE_INCREMENT,
  SORT_OPTIONS,
} from "./constants";
import type { SortField, SortOrder } from "./types";
import { RequestRow } from "./RequestRow";

// =============================================================================
// Flat Request List (All View)
// =============================================================================

export interface FlatRequestListProps {
  requests: ActiveRequest[];
  currentTime: number;
  longRunningThreshold: number;
  providerNames?: Map<string, string>;
  sortField: SortField;
  sortOrder: SortOrder;
  onSortChange: (field: SortField) => void;
}

export function FlatRequestList({
  requests,
  currentTime,
  longRunningThreshold,
  providerNames,
  sortField,
  sortOrder,
  onSortChange,
}: FlatRequestListProps) {
  const [visibleCount, setVisibleCount] = useState(INITIAL_VISIBLE_COUNT);

  const visibleRequests = requests.slice(0, visibleCount);
  const hasMore = requests.length > visibleCount;

  if (requests.length === 0) {
    return (
      <div className="text-center py-8 text-text-secondary">
        No requests match your search
      </div>
    );
  }

  return (
    <div>
      {/* Sort controls */}
      <div className="flex items-center gap-2 mb-3 text-sm">
        <span className="text-text-muted">Sort:</span>
        <div className="flex gap-1" role="group" aria-label="Sort options">
          {SORT_OPTIONS.map((opt) => (
            <button
              key={opt.field}
              onClick={() => onSortChange(opt.field)}
              aria-pressed={sortField === opt.field}
              className={`px-2 py-1 rounded text-xs font-medium transition-colors ${
                sortField === opt.field
                  ? "bg-primary text-white"
                  : "bg-bg-tertiary text-text-secondary hover:bg-bg-hover"
              }`}
            >
              {opt.label}
              {sortField === opt.field && (
                <span className="ml-0.5">
                  {sortOrder === "desc" ? "↓" : "↑"}
                </span>
              )}
            </button>
          ))}
        </div>
        <span className="text-text-muted ml-auto">
          Showing {Math.min(visibleCount, requests.length)} of {requests.length}
        </span>
      </div>

      {/* Request list */}
      <ul className="border border-border rounded-lg divide-y divide-border-light overflow-hidden list-none m-0 p-0">
        {visibleRequests.map((req) => (
          <li key={req.request_id}>
            <RequestRow
              request={req}
              currentTime={currentTime}
              longRunningThreshold={longRunningThreshold}
              providerName={providerNames?.get(req.provider_id)}
            />
          </li>
        ))}
      </ul>

      {/* Load More */}
      {hasMore && (
        <div className="flex items-center justify-center gap-4 p-3 text-sm">
          <button
            onClick={() =>
              setVisibleCount((prev) =>
                Math.min(prev + LOAD_MORE_INCREMENT, requests.length),
              )
            }
            className="text-primary hover:underline font-medium"
            aria-label={`Load ${Math.min(LOAD_MORE_INCREMENT, requests.length - visibleCount)} more requests`}
          >
            Load {Math.min(LOAD_MORE_INCREMENT, requests.length - visibleCount)}{" "}
            more
          </button>
          <span className="text-text-muted">or</span>
          <button
            onClick={() => setVisibleCount(requests.length)}
            className="text-primary hover:underline font-medium"
            aria-label={`Show all ${requests.length} requests`}
          >
            Show all {requests.length}
          </button>
        </div>
      )}
    </div>
  );
}
