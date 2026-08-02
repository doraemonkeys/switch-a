import { useState, type FormEvent } from "react";
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

  const clearPreparedExport = () => {
    setGrant(null);
    setPreparedLabel("");
  };

  const handleDownloadSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();

    const sourceForm = event.currentTarget;
    const tokenInput = sourceForm.elements.namedItem("download_token");
    if (!(tokenInput instanceof HTMLInputElement) || tokenInput.value === "") {
      return;
    }

    // The rendered form is part of React state, so mutating it during submit can
    // race the browser's native form serialization. Cloning the validated
    // capability into a transient native form keeps the actual POST on the
    // browser side while letting us clear the visible grant immediately.
    const downloadForm = document.createElement("form");
    downloadForm.method = sourceForm.method;
    downloadForm.action = sourceForm.action;
    downloadForm.style.display = "none";

    const downloadTokenField = document.createElement("input");
    downloadTokenField.type = "hidden";
    downloadTokenField.name = "download_token";
    downloadTokenField.value = tokenInput.value;
    downloadForm.appendChild(downloadTokenField);

    document.body.appendChild(downloadForm);
    downloadForm.submit();
    window.setTimeout(() => downloadForm.remove(), 0);

    clearPreparedExport();
  };

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
          Exports are NDJSON snapshots containing raw payloads. Download tokens
          are short-lived and single-use.
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
          <form
            className="mt-3"
            action={grant.download_path}
            method="post"
            onSubmit={handleDownloadSubmit}
          >
            <input
              type="hidden"
              name="download_token"
              value={grant.download_token}
            />
            <button type="submit" className="btn btn-primary">
              <Download className="h-4 w-4" aria-hidden="true" />
              Download NDJSON
            </button>
          </form>
        </div>
      )}
    </section>
  );
}
