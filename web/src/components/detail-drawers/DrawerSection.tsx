import type { ReactNode } from "react";

interface DetailSectionProps {
  title: string;
  children: ReactNode;
  action?: ReactNode;
}

/**
 * A reusable section component for drawer panels.
 * Displays a title with optional action button and content area.
 */
export function DetailSection({ title, children, action }: DetailSectionProps) {
  return (
    <div className="border-t border-border-light pt-4">
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-semibold text-text-muted uppercase tracking-wide">
          {title}
        </h3>
        {action}
      </div>
      <div className="space-y-2">{children}</div>
    </div>
  );
}

interface DetailRowProps {
  label: string;
  value: ReactNode;
  /** If true, applies monospace font styling */
  mono?: boolean;
}

/**
 * A reusable row component for displaying label-value pairs in drawers.
 */
export function DetailRow({ label, value, mono }: DetailRowProps) {
  const textClass = mono ? "font-mono text-xs" : "font-medium";
  return (
    <div className="flex items-start justify-between py-1">
      <span className="text-sm text-text-secondary">{label}</span>
      <span
        className={`text-sm text-text-primary text-right max-w-[60%] wrap-break-word ${textClass}`}
      >
        {value}
      </span>
    </div>
  );
}
