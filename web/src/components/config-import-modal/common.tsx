export const CloseButton = ({
  onClick,
  disabled,
}: {
  onClick: () => void;
  disabled?: boolean;
}) => (
  <button
    onClick={onClick}
    disabled={disabled}
    className="text-text-secondary hover:text-text-primary transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
    aria-label="Close"
  >
    <svg
      className="w-5 h-5"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={2}
        d="M6 18L18 6M6 6l12 12"
      />
    </svg>
  </button>
);

export function FileCard({
  selectedFile,
  actionLabel,
  onAction,
  disabled,
}: {
  selectedFile: File;
  actionLabel: string;
  onAction: () => void;
  disabled?: boolean;
}) {
  return (
    <div className="flex items-center gap-3 p-3 bg-bg-tertiary rounded-lg">
      <svg
        className="w-5 h-5 text-primary shrink-0"
        fill="none"
        viewBox="0 0 24 24"
      >
        <path
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth={2}
          d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"
        />
      </svg>
      <div className="min-w-0 flex-1">
        <p className="text-sm text-text-primary font-medium truncate">
          {selectedFile.name}
        </p>
        <p className="text-xs text-text-muted">
          {(selectedFile.size / 1024).toFixed(1)} KB
        </p>
      </div>
      <button
        onClick={onAction}
        disabled={disabled}
        className="text-xs text-text-secondary hover:text-primary transition-colors disabled:opacity-50"
      >
        {actionLabel}
      </button>
    </div>
  );
}
