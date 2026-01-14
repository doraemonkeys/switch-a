import { useState, useEffect } from "react";
import {
  INITIAL_VISIBLE_COUNT,
  LOAD_MORE_INCREMENT,
  VIEW_MODE_LABELS,
  SORT_OPTIONS,
  CURRENT_TIME_REFRESH_INTERVAL_MS,
} from "./constants";
import type {
  GroupViewMode,
  SortField,
  SortOrder,
  LiveRequestsPanelProps,
} from "./types";
import { filterRequests, groupRequests, sortRequests } from "./utils";
import { FlatRequestList } from "./FlatRequestList";
import { GroupedRequestList } from "./GroupedRequestList";

// =============================================================================
// Main Component
// =============================================================================

export function LiveRequestsPanel({
  requests,
  loading,
  error,
  longRunningThreshold = 300000,
  providerNames,
}: LiveRequestsPanelProps) {
  // Current timestamp for duration calculation
  const [currentTime, setCurrentTime] = useState(() => Date.now());

  // View state
  const [viewMode, setViewMode] = useState<GroupViewMode>("ip");
  const [searchQuery, setSearchQuery] = useState("");
  const [sortField, setSortField] = useState<SortField>("duration");
  const [sortOrder, setSortOrder] = useState<SortOrder>("desc");

  // Expanded groups state (persisted across refreshes)
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set());

  // Visible count per group for progressive loading
  const [visibleCounts, setVisibleCounts] = useState<Map<string, number>>(
    new Map(),
  );

  // Update current time every second
  useEffect(() => {
    const interval = setInterval(
      () => setCurrentTime(Date.now()),
      CURRENT_TIME_REFRESH_INTERVAL_MS,
    );
    return () => clearInterval(interval);
  }, []);

  // Derived state: Filter requests by search
  const filteredRequests = filterRequests(requests, searchQuery);

  // Derived state: Group and sort requests
  const groups = groupRequests(
    filteredRequests,
    viewMode,
    currentTime,
    longRunningThreshold,
  );

  // Derived state: All requests sorted (for "all" view mode)
  const allSortedRequests = sortRequests(
    filteredRequests,
    sortField,
    sortOrder,
    currentTime,
  );

  // Toggle group expansion
  const toggleGroup = (groupKey: string) => {
    setExpandedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(groupKey)) {
        next.delete(groupKey);
      } else {
        next.add(groupKey);
      }
      return next;
    });
  };

  // Get visible count for a group
  const getVisibleCount = (groupKey: string) =>
    visibleCounts.get(groupKey) || INITIAL_VISIBLE_COUNT;

  // Load more for a group
  const loadMore = (groupKey: string, totalCount: number) => {
    setVisibleCounts((prev) => {
      const next = new Map(prev);
      const current = next.get(groupKey) || INITIAL_VISIBLE_COUNT;
      next.set(groupKey, Math.min(current + LOAD_MORE_INCREMENT, totalCount));
      return next;
    });
  };

  // Show all for a group
  const showAll = (groupKey: string, totalCount: number) => {
    setVisibleCounts((prev) => {
      const next = new Map(prev);
      next.set(groupKey, totalCount);
      return next;
    });
  };

  // Toggle sort order or change field
  const handleSortChange = (field: SortField) => {
    setSortField((prevField) => {
      if (prevField === field) {
        setSortOrder((prev) => (prev === "desc" ? "asc" : "desc"));
        return field;
      }
      setSortOrder("desc");
      return field;
    });
  };

  // Error state
  if (error) {
    return (
      <div className="p-4 text-center text-red-500 dark:text-red-400">
        Failed to load active requests: {error.message}
      </div>
    );
  }

  // Loading state
  if (loading && requests.length === 0) {
    return (
      <div className="p-8 text-center">
        <div className="inline-block animate-spin rounded-full h-8 w-8 border-b-2 border-accent-primary" />
        <p className="mt-2 text-text-secondary">Loading active requests...</p>
      </div>
    );
  }

  // Empty state
  if (requests.length === 0) {
    return (
      <div className="p-8 text-center text-text-secondary">
        <svg
          className="mx-auto h-12 w-12 text-text-muted mb-4"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
          aria-hidden="true"
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth={1.5}
            d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4"
          />
        </svg>
        <p className="text-lg font-medium text-text-primary">
          No active requests
        </p>
        <p className="mt-1 text-sm">
          Requests will appear here when they are in progress
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {/* Header with summary and controls */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-2 text-sm text-text-secondary">
          <span className="font-semibold text-text-primary text-lg">
            {filteredRequests.length}
          </span>
          <span>Active Requests</span>
          {searchQuery && (
            <span className="text-text-muted">
              (filtered from {requests.length})
            </span>
          )}
        </div>

        {/* Search */}
        <div className="relative flex-1 max-w-xs">
          <input
            type="text"
            placeholder="Search IP, model, user..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="input pl-9 py-1.5 text-sm"
            aria-label="Search active requests by IP, model, or user"
          />
          <svg
            className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-text-muted"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
            />
          </svg>
          {searchQuery && (
            <button
              onClick={() => setSearchQuery("")}
              className="absolute right-2 top-1/2 -translate-y-1/2 p-1 hover:bg-bg-hover rounded"
              aria-label="Clear search"
            >
              <svg
                className="h-3 w-3 text-text-muted"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
                aria-hidden="true"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth={2}
                  d="M6 18L18 6M6 6l12 12"
                />
              </svg>
            </button>
          )}
        </div>
      </div>

      {/* View mode tabs */}
      <div className="flex gap-1 p-1 bg-bg-tertiary rounded-lg" role="tablist">
        {(Object.keys(VIEW_MODE_LABELS) as GroupViewMode[]).map((mode) => (
          <button
            key={mode}
            role="tab"
            aria-selected={viewMode === mode}
            onClick={() => setViewMode(mode)}
            className={`flex-1 flex items-center justify-center gap-1.5 px-3 py-2 text-sm font-medium rounded-md transition-all ${
              viewMode === mode
                ? "bg-white text-text-primary shadow-sm"
                : "text-text-secondary hover:text-text-primary hover:bg-bg-hover"
            }`}
          >
            <span>{VIEW_MODE_LABELS[mode].icon}</span>
            <span className="hidden sm:inline">
              {VIEW_MODE_LABELS[mode].label}
            </span>
          </button>
        ))}
      </div>

      {/* Sort controls (shown in grouped modes) */}
      {viewMode !== "all" && groups.length > 0 && (
        <div className="flex items-center gap-2 text-sm">
          <span className="text-text-muted">Sort within groups:</span>
          <div className="flex gap-1">
            {SORT_OPTIONS.map((opt) => (
              <button
                key={opt.field}
                onClick={() => handleSortChange(opt.field)}
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
        </div>
      )}

      {/* Content */}
      {viewMode === "all" ? (
        <FlatRequestList
          requests={allSortedRequests}
          currentTime={currentTime}
          longRunningThreshold={longRunningThreshold}
          providerNames={providerNames}
          sortField={sortField}
          sortOrder={sortOrder}
          onSortChange={handleSortChange}
        />
      ) : (
        <GroupedRequestList
          groups={groups}
          expandedGroups={expandedGroups}
          toggleGroup={toggleGroup}
          currentTime={currentTime}
          longRunningThreshold={longRunningThreshold}
          providerNames={providerNames}
          sortField={sortField}
          sortOrder={sortOrder}
          getVisibleCount={getVisibleCount}
          loadMore={loadMore}
          showAll={showAll}
          groupBy={viewMode}
        />
      )}
    </div>
  );
}
