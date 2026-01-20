import { useState } from "react";

interface PaginationProps {
  currentPage: number;
  totalPages: number;
  startResult: number;
  endResult: number;
  total: number;
  loading: boolean;
  onPageChange: (page: number) => void;
}

export function Pagination({
  currentPage,
  totalPages,
  startResult,
  endResult,
  total,
  loading,
  onPageChange,
}: PaginationProps) {
  const [jumpPage, setJumpPage] = useState("");

  const handleJumpSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const page = parseInt(jumpPage, 10);
    if (!isNaN(page) && page >= 1 && page <= totalPages) {
      onPageChange(page);
      setJumpPage("");
    }
  };

  const pageNumbers = (() => {
    const pages: (number | "...")[] = [];
    const showPages = 5;

    if (totalPages <= showPages + 2) {
      for (let i = 1; i <= totalPages; i++) {
        pages.push(i);
      }
    } else {
      pages.push(1);
      let start = Math.max(2, currentPage - 1);
      let end = Math.min(totalPages - 1, currentPage + 1);

      if (currentPage <= 3) {
        end = Math.min(showPages - 1, totalPages - 1);
      }
      if (currentPage >= totalPages - 2) {
        start = Math.max(2, totalPages - showPages + 2);
      }
      if (start > 2) {
        pages.push("...");
      }
      for (let i = start; i <= end; i++) {
        pages.push(i);
      }
      if (end < totalPages - 1) {
        pages.push("...");
      }
      pages.push(totalPages);
    }
    return pages;
  })();

  return (
    <div className="flex flex-col sm:flex-row items-center justify-between gap-4">
      <p className="text-sm text-text-secondary">
        Showing{" "}
        <span className="font-medium text-text-primary">{startResult}</span> to{" "}
        <span className="font-medium text-text-primary">{endResult}</span> of{" "}
        <span className="font-medium text-text-primary">{total}</span> results
      </p>

      <div className="flex items-center gap-2">
        <button
          onClick={() => onPageChange(1)}
          disabled={currentPage === 1 || loading}
          className="btn btn-secondary btn-sm disabled:opacity-50 disabled:cursor-not-allowed"
          title="First page"
        >
          ««
        </button>
        <button
          onClick={() => onPageChange(currentPage - 1)}
          disabled={currentPage === 1 || loading}
          className="btn btn-secondary btn-sm disabled:opacity-50 disabled:cursor-not-allowed"
        >
          ←
        </button>

        <div className="hidden sm:flex items-center gap-1">
          {pageNumbers.map((page, index) =>
            page === "..." ? (
              <span
                key={`ellipsis-after-${pageNumbers[index - 1]}`}
                className="px-2 text-text-muted"
              >
                ...
              </span>
            ) : (
              <button
                key={page}
                onClick={() => onPageChange(page)}
                disabled={loading}
                className={`px-3 py-1 text-sm font-medium rounded-lg transition-colors ${
                  page === currentPage
                    ? "bg-primary text-white"
                    : "text-text-secondary hover:bg-bg-tertiary"
                }`}
              >
                {page}
              </button>
            ),
          )}
        </div>

        <span className="sm:hidden px-3 py-1 bg-primary text-white text-sm font-medium rounded-lg">
          {currentPage} / {totalPages}
        </span>

        <button
          onClick={() => onPageChange(currentPage + 1)}
          disabled={currentPage === totalPages || loading}
          className="btn btn-secondary btn-sm disabled:opacity-50 disabled:cursor-not-allowed"
        >
          →
        </button>
        <button
          onClick={() => onPageChange(totalPages)}
          disabled={currentPage === totalPages || loading}
          className="btn btn-secondary btn-sm disabled:opacity-50 disabled:cursor-not-allowed"
          title="Last page"
        >
          »»
        </button>

        {totalPages > 5 && (
          <form
            onSubmit={handleJumpSubmit}
            className="hidden md:flex items-center gap-2 ml-2"
          >
            <label htmlFor="go-to-page" className="text-sm text-text-muted">
              Go to
            </label>
            <input
              id="go-to-page"
              type="number"
              min={1}
              max={totalPages}
              value={jumpPage}
              onChange={(e) => setJumpPage(e.target.value)}
              placeholder="#"
              className="input input-sm w-16 text-center"
            />
          </form>
        )}
      </div>
    </div>
  );
}
