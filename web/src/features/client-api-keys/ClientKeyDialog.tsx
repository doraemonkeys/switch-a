import { useEffect, useId, useRef, type ReactNode } from "react";
import { X } from "lucide-react";

const INITIAL_FOCUS_SELECTOR = "[data-key-autofocus]";

interface Props {
  title: string;
  description: string;
  busy: boolean;
  onClose: () => void;
  children: ReactNode;
}

export function ClientKeyDialog({
  title,
  description,
  busy,
  onClose,
  children,
}: Props) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const descriptionId = useId();

  useEffect(() => {
    const dialog = dialogRef.current;
    const invoker = document.activeElement;
    // The native modal keeps focus and pointer interaction inside the active task.
    dialog?.showModal();
    dialog?.querySelector<HTMLElement>(INITIAL_FOCUS_SELECTOR)?.focus();
    return () => {
      dialog?.close();
      if (invoker instanceof HTMLElement && invoker.isConnected)
        invoker.focus();
    };
  }, []);

  return (
    <dialog
      ref={dialogRef}
      className="client-keys-dialog"
      aria-labelledby={titleId}
      aria-describedby={descriptionId}
      aria-busy={busy}
      onCancel={(event) => {
        event.preventDefault();
        if (!busy) onClose();
      }}
    >
      <header className="flex items-start justify-between gap-4 border-b border-slate-200 px-6 py-5">
        <div className="min-w-0">
          <h2
            id={titleId}
            className="break-words text-xl font-semibold tracking-tight"
          >
            {title}
          </h2>
          <p
            id={descriptionId}
            className="mt-2 text-sm leading-6 text-slate-500"
          >
            {description}
          </p>
        </div>
        <button
          type="button"
          className="key-icon-button shrink-0"
          aria-label="Close"
          disabled={busy}
          onClick={onClose}
        >
          <X size={18} aria-hidden="true" />
        </button>
      </header>
      {children}
    </dialog>
  );
}
