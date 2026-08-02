import { useEffect, useEffectEvent } from "react";
import { AlertTriangle, X } from "lucide-react";

interface StopCaptureDialogProps {
  open: boolean;
  sessionId: string;
  stopping: boolean;
  error: string | null;
  onClose: () => void;
  onConfirm: () => void;
}

export function StopCaptureDialog({
  open,
  sessionId,
  stopping,
  error,
  onClose,
  onConfirm,
}: StopCaptureDialogProps) {
  const closeOnEscape = useEffectEvent((event: KeyboardEvent) => {
    if (event.key === "Escape" && !stopping) onClose();
  });

  useEffect(() => {
    if (!open) return;
    document.addEventListener("keydown", closeOnEscape);
    return () => document.removeEventListener("keydown", closeOnEscape);
  }, [open, stopping]);

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-labelledby="stop-capture-title"
    >
      <div className="w-full max-w-lg rounded-xl border border-border bg-white shadow-2xl">
        <header className="flex items-start justify-between gap-4 border-b border-border p-5">
          <div className="flex items-start gap-3">
            <AlertTriangle
              className="mt-0.5 h-5 w-5 shrink-0 text-danger"
              aria-hidden="true"
            />
            <div>
              <h2
                id="stop-capture-title"
                className="text-lg font-bold text-text-primary"
              >
                Stop and clear Debug Capture?
              </h2>
              <p className="mt-1 truncate font-mono text-xs text-text-muted">
                {sessionId}
              </p>
            </div>
          </div>
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={onClose}
            disabled={stopping}
            aria-label="Close stop confirmation"
          >
            <X className="h-5 w-5" aria-hidden="true" />
          </button>
        </header>
        <div className="space-y-3 p-5 text-sm text-text-secondary">
          <p>
            All records from this session become immediately unavailable.
            Pending exports and active downloads are canceled.
          </p>
          <p>
            Requests already passing through the proxy continue normally; Stop
            only disables the side-channel capture.
          </p>
          {error && (
            <p role="alert" className="font-medium text-danger">
              {error}
            </p>
          )}
        </div>
        <footer className="flex justify-end gap-3 border-t border-border p-5">
          <button
            type="button"
            className="btn btn-secondary"
            onClick={onClose}
            disabled={stopping}
          >
            Keep capturing
          </button>
          <button
            type="button"
            className="btn btn-danger"
            onClick={onConfirm}
            disabled={stopping}
          >
            {stopping ? "Stopping…" : "Stop and clear"}
          </button>
        </footer>
      </div>
    </div>
  );
}
