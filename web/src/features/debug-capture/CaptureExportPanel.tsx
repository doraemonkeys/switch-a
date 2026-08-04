import { useState } from "react";
import { Download, FileDown } from "lucide-react";
import { useApi, type CreateDebugCaptureExportRequest } from "@/api";
import {
  validatedDebugCaptureDownloadGrant,
  type ValidatedDebugCaptureDownloadGrant,
} from "./downloadGrant";

interface CaptureExportPanelProps {
  sessionId: string;
  totalRecords: number;
  selectedRecordIds: ReadonlySet<string>;
}

export function CaptureExportPanel({
  sessionId,
  totalRecords,
  selectedRecordIds,
}: CaptureExportPanelProps) {
  const api = useApi();
  const [preparing, setPreparing] = useState(false);
  const [grant, setGrant] = useState<ValidatedDebugCaptureDownloadGrant | null>(
    null,
  );
  const [preparedLabel, setPreparedLabel] = useState("");
  const [error, setError] = useState<string | null>(null);

  const prepareExport = async (
    request: CreateDebugCaptureExportRequest,
    label: string,
  ) => {
    setPreparing(true);
    setGrant(null);
    setError(null);
    try {
      const nextGrant = await api.debugCapture.createExport(sessionId, request);
      const validatedGrant = validatedDebugCaptureDownloadGrant(
        nextGrant,
        sessionId,
      );
      if (validatedGrant === null) {
        throw new Error("The server returned an invalid download grant.");
      }
      setGrant(validatedGrant);
      setPreparedLabel(label);
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Unable to prepare export.",
      );
    } finally {
      setPreparing(false);
    }
  };

  const selectedIds = Array.from(selectedRecordIds);

  return (
    <section className="card space-y-4" aria-labelledby="capture-export-title">
      <div>
        <h3
          id="capture-export-title"
          className="text-lg font-semibold text-text-primary"
        >
          Export capture
        </h3>
        <p className="mt-1 text-sm text-text-secondary">
          Exports are NDJSON snapshots containing raw payloads. Selected records
          are bundled into one file, and the short-lived link can be retried or
          handed to an external download manager until it expires.
        </p>
      </div>

      <div className="flex flex-wrap gap-3">
        <button
          type="button"
          className="btn btn-secondary"
          disabled={preparing || totalRecords === 0}
          onClick={() =>
            void prepareExport(
              { scope: "all" },
              "all " + totalRecords + " records",
            )
          }
        >
          <FileDown className="h-4 w-4" aria-hidden="true" />
          Prepare all
        </button>
        <button
          type="button"
          className="btn btn-secondary"
          disabled={preparing || selectedIds.length === 0}
          onClick={() =>
            void prepareExport(
              { scope: "records", record_ids: selectedIds },
              selectedIds.length + " selected records",
            )
          }
        >
          <FileDown className="h-4 w-4" aria-hidden="true" />
          Prepare selected ({selectedIds.length})
        </button>
      </div>

      {preparing && <p role="status">Preparing immutable export snapshot…</p>}
      {error && (
        <p role="alert" className="text-sm font-medium text-danger">
          {error}
        </p>
      )}

      {grant && (
        <div className="rounded-lg border border-success/30 bg-success-light p-4">
          <p className="text-sm text-text-primary">
            Ready to download {preparedLabel}. Token expires{" "}
            {new Date(grant.expires_at).toLocaleString()}.
          </p>
          <a
            className="btn btn-primary mt-3"
            href={grant.download_url}
            download={`switch-a-debug-capture-${grant.export_id}.ndjson`}
            referrerPolicy="no-referrer"
          >
            <Download className="h-4 w-4" aria-hidden="true" />
            Download NDJSON
          </a>
        </div>
      )}
    </section>
  );
}
