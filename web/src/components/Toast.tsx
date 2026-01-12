import { useState, useCallback, useEffect, type ReactNode } from "react";
import {
  ToastContext,
  type Toast,
  type ToastType,
  type ToastContextValue,
} from "../hooks/useToast";

// Re-export types for backward compatibility
export type { Toast, ToastType } from "../hooks/useToast";

// =====================
// Icons
// =====================
const icons: Record<ToastType, ReactNode> = {
  success: (
    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
    </svg>
  ),
  error: (
    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
    </svg>
  ),
  warning: (
    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
    </svg>
  ),
  info: (
    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
    </svg>
  ),
};

// =====================
// Toast Item Component
// =====================
interface ToastItemProps {
  toast: Toast;
  onRemove: (id: string) => void;
}

const typeStyles: Record<ToastType, string> = {
  success: "bg-emerald-50 border-emerald-200 text-emerald-800",
  error: "bg-red-50 border-red-200 text-red-800",
  warning: "bg-amber-50 border-amber-200 text-amber-800",
  info: "bg-blue-50 border-blue-200 text-blue-800",
};

const iconBgStyles: Record<ToastType, string> = {
  success: "bg-emerald-100 text-emerald-600",
  error: "bg-red-100 text-red-600",
  warning: "bg-amber-100 text-amber-600",
  info: "bg-blue-100 text-blue-600",
};

function ToastItem({ toast, onRemove }: ToastItemProps) {
  const [isExiting, setIsExiting] = useState(false);

  useEffect(() => {
    if (toast.duration === 0) return; // duration 0 means no auto-dismiss

    const duration = toast.duration ?? 4000;
    let exitTimer: ReturnType<typeof setTimeout>;

    const timer = setTimeout(() => {
      setIsExiting(true);
      exitTimer = setTimeout(() => onRemove(toast.id), 300);
    }, duration);

    return () => {
      clearTimeout(timer);
      clearTimeout(exitTimer);
    };
  }, [toast, onRemove]);

  const handleClose = () => {
    if (isExiting) return; // Prevent double-close
    setIsExiting(true);
    setTimeout(() => onRemove(toast.id), 300);
  };

  return (
    <div
      className={`
        flex items-start gap-3 px-4 py-3 rounded-xl border shadow-lg
        backdrop-blur-sm min-w-[320px] max-w-[420px]
        transition-all duration-300 ease-out
        ${typeStyles[toast.type]}
        ${isExiting ? "toast-exit" : "toast-enter"}
      `}
      role="alert"
    >
      <div className={`flex-shrink-0 p-1.5 rounded-lg ${iconBgStyles[toast.type]}`}>
        {icons[toast.type]}
      </div>
      <p className="flex-1 text-sm font-medium leading-relaxed pt-1">{toast.message}</p>
      <button
        onClick={handleClose}
        className="flex-shrink-0 p-1 rounded-lg opacity-60 hover:opacity-100 transition-opacity cursor-pointer"
        aria-label="关闭通知"
      >
        <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
        </svg>
      </button>
    </div>
  );
}

// =====================
// Toast Container
// =====================
interface ToastContainerProps {
  toasts: Toast[];
  onRemove: (id: string) => void;
}

function ToastContainer({ toasts, onRemove }: ToastContainerProps) {
  if (toasts.length === 0) return null;

  return (
    <div className="fixed top-4 right-4 z-[9999] flex flex-col gap-3">
      {toasts.map((toast) => (
        <ToastItem key={toast.id} toast={toast} onRemove={onRemove} />
      ))}
    </div>
  );
}

// =====================
// Provider
// =====================
interface ToastProviderProps {
  children: ReactNode;
}

let toastIdCounter = 0;

export function ToastProvider({ children }: ToastProviderProps) {
  const [toasts, setToasts] = useState<Toast[]>([]);

  const removeToast = useCallback((id: string) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const addToast = useCallback((toast: Omit<Toast, "id">): string => {
    const id = `toast-${++toastIdCounter}`;
    setToasts((prev) => [...prev, { ...toast, id }]);
    return id;
  }, []);

  const success = useCallback(
    (message: string, duration?: number) =>
      addToast({ type: "success", message, duration }),
    [addToast]
  );

  const error = useCallback(
    (message: string, duration?: number) =>
      addToast({ type: "error", message, duration }),
    [addToast]
  );

  const warning = useCallback(
    (message: string, duration?: number) =>
      addToast({ type: "warning", message, duration }),
    [addToast]
  );

  const info = useCallback(
    (message: string, duration?: number) =>
      addToast({ type: "info", message, duration }),
    [addToast]
  );

  const value: ToastContextValue = {
    toasts,
    addToast,
    removeToast,
    success,
    error,
    warning,
    info,
  };

  return (
    <ToastContext.Provider value={value}>
      {children}
      <ToastContainer toasts={toasts} onRemove={removeToast} />
    </ToastContext.Provider>
  );
}
