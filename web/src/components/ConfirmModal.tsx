import { useEffect, useCallback } from "react";

interface ConfirmModalProps {
    isOpen: boolean;
    onClose: () => void;
    onConfirm: () => void;
    title: string;
    message: string;
    confirmText?: string;
    cancelText?: string;
    variant?: "danger" | "warning" | "default";
    loading?: boolean;
}

export function ConfirmModal({
    isOpen,
    onClose,
    onConfirm,
    title,
    message,
    confirmText = "Confirm",
    cancelText = "Cancel",
    variant = "default",
    loading = false,
}: ConfirmModalProps) {
    // Handle Escape key to close modal
    const handleEscape = useCallback(
        (e: KeyboardEvent) => {
            if (e.key === "Escape" && !loading) {
                onClose();
            }
        },
        [onClose, loading]
    );

    useEffect(() => {
        if (isOpen) {
            document.addEventListener("keydown", handleEscape);
            return () => document.removeEventListener("keydown", handleEscape);
        }
    }, [isOpen, handleEscape]);

    if (!isOpen) return null;

    const variantStyles = {
        danger: "bg-red-500 hover:bg-red-600 text-white",
        warning: "bg-yellow-500 hover:bg-yellow-600 text-white",
        default: "btn-primary",
    };

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-sm">
            <div className="bg-bg-secondary w-full max-w-md rounded-xl shadow-2xl border border-border-light">
                <div className="p-6 border-b border-border-light flex justify-between items-center">
                    <h2 className="text-xl font-bold text-text-primary">{title}</h2>
                    <button
                        onClick={onClose}
                        disabled={loading}
                        className="text-text-secondary hover:text-text-primary transition-colors disabled:opacity-50"
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
                </div>

                <div className="p-6">
                    <p className="text-text-secondary">{message}</p>
                </div>

                <div className="flex justify-end gap-3 p-6 pt-0">
                    <button
                        type="button"
                        onClick={onClose}
                        disabled={loading}
                        className="px-4 py-2 text-text-secondary hover:text-text-primary transition-colors disabled:opacity-50"
                    >
                        {cancelText}
                    </button>
                    <button
                        type="button"
                        onClick={onConfirm}
                        disabled={loading}
                        className={`px-4 py-2 rounded-lg font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed ${variantStyles[variant]}`}
                    >
                        {loading ? "Processing..." : confirmText}
                    </button>
                </div>
            </div>
        </div>
    );
}
