import { useDebugCapture } from "./useDebugCapture";

export function DebugCaptureStatusBadge() {
  const { status } = useDebugCapture();
  if (status?.state !== "active") return null;

  return (
    <span className="ml-auto inline-flex items-center gap-1.5 rounded-full bg-danger-light px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-danger">
      <span
        className="h-1.5 w-1.5 animate-pulse rounded-full bg-danger"
        aria-hidden="true"
      />
      Active
    </span>
  );
}
