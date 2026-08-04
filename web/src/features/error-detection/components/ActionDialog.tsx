import { useEffect, useId, useRef, type KeyboardEvent } from "react";

export interface ActionDialogProps {
  readonly open: boolean;
  readonly title: string;
  readonly description: string;
  readonly confirmLabel: string;
  readonly cancelLabel?: string;
  readonly danger?: boolean;
  readonly busy?: boolean;
  readonly onConfirm: () => void;
  readonly onCancel: () => void;
}

const FOCUSABLE_CONTROL_SELECTOR =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

export function ActionDialog({
  open,
  title,
  description,
  confirmLabel,
  cancelLabel = "Cancel",
  danger = false,
  busy = false,
  onConfirm,
  onCancel,
}: ActionDialogProps) {
  const titleID = useId();
  const descriptionID = useId();
  const dialogRef = useRef<HTMLDivElement>(null);
  const cancelButtonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const invoker =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    if (cancelButtonRef.current && !cancelButtonRef.current.disabled) {
      cancelButtonRef.current.focus();
    } else {
      dialogRef.current?.focus();
    }
    return () => {
      // Returning to the invoker prevents a closed modal from stranding keyboard
      // users on document.body; deleted invokers are intentionally ignored.
      if (invoker?.isConnected) invoker.focus();
    };
  }, [open]);

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape" && !busy) {
      event.preventDefault();
      onCancel();
      return;
    }
    if (event.key !== "Tab") return;
    const controls = Array.from(
      event.currentTarget.querySelectorAll<HTMLElement>(
        FOCUSABLE_CONTROL_SELECTOR,
      ),
    );
    const first = controls[0];
    const last = controls.at(-1);
    if (!first || !last) {
      event.preventDefault();
      dialogRef.current?.focus();
    } else if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm">
      <div
        ref={dialogRef}
        role="alertdialog"
        aria-modal="true"
        aria-busy={busy}
        aria-labelledby={titleID}
        aria-describedby={descriptionID}
        tabIndex={-1}
        onKeyDown={handleKeyDown}
        className="w-full max-w-md rounded-2xl border border-border bg-white p-6 shadow-2xl"
      >
        <h2 id={titleID} className="text-lg font-semibold text-text-primary">
          {title}
        </h2>
        <p id={descriptionID} className="mt-2 text-sm text-text-secondary">
          {description}
        </p>
        <div className="mt-6 flex justify-end gap-3">
          <button
            ref={cancelButtonRef}
            type="button"
            disabled={busy}
            onClick={onCancel}
            className="btn btn-secondary"
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={onConfirm}
            className={danger ? "btn bg-danger text-white" : "btn btn-primary"}
          >
            {busy ? "Working…" : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
