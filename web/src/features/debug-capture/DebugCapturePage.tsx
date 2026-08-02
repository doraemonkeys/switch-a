import { Bug, RefreshCw } from "lucide-react";
import { StartCaptureForm } from "./StartCaptureForm";
import { ActiveCaptureSession } from "./ActiveCaptureSession";
import { useDebugCapture } from "./useDebugCapture";

export function DebugCapturePage() {
  const { status, loading, error, operation, refreshStatus, startCapture } =
    useDebugCapture();

  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-start justify-between gap-4 rounded-2xl border border-border bg-white p-6 shadow-sm">
        <div className="flex items-start gap-4">
          <div className="rounded-xl bg-primary-light p-3 text-primary">
            <Bug className="h-6 w-6" aria-hidden="true" />
          </div>
          <div>
            <h1 className="text-2xl font-bold tracking-tight text-text-primary">
              Debug Capture
            </h1>
            <p className="mt-1 text-sm text-text-secondary">
              Capture bounded, in-memory upstream exchanges without changing
              proxy behavior.
            </p>
          </div>
        </div>
        <button
          type="button"
          className="btn btn-secondary"
          onClick={() => void refreshStatus()}
          disabled={loading}
        >
          <RefreshCw
            className={"h-4 w-4" + (loading ? " animate-spin" : "")}
            aria-hidden="true"
          />
          Refresh status
        </button>
      </header>

      {error && status && (
        <p
          role="alert"
          className="rounded-lg border border-warning/30 bg-warning-light p-3 text-sm text-text-secondary"
        >
          Status could not refresh. The last confirmed snapshot remains visible:{" "}
          {error.message}
        </p>
      )}

      {loading && !status && (
        <div className="card" role="status">
          Loading Debug Capture status…
        </div>
      )}

      {error && !status && (
        <div className="card space-y-3" role="alert">
          <p className="font-medium text-danger">{error.message}</p>
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => void refreshStatus()}
          >
            Retry
          </button>
        </div>
      )}

      {status?.state === "stopped" && (
        <StartCaptureForm
          processCeilingBytes={status.process_memory.ceiling_bytes}
          starting={operation === "start"}
          onStart={startCapture}
        />
      )}

      {status?.state === "active" && status.session && (
        <ActiveCaptureSession
          key={status.session.session_id}
          status={status}
          session={status.session}
        />
      )}

      {status?.state === "active" && !status.session && (
        <p role="alert" className="card text-danger">
          The server reported an active capture without session details.
        </p>
      )}
    </div>
  );
}
