import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { RequestEvidenceViewer } from "./RequestEvidenceViewer";

afterEach(cleanup);

describe("response facts", () => {
  it("shows upstream completion independently of a failed downstream write and disconnect", () => {
    render(
      <RequestEvidenceViewer
        evidenceJson={JSON.stringify({
          v: 2,
          upstream_completion: {
            event_type: "response.completed",
            observed_at: "2026-09-08T11:00:00Z",
          },
          downstream_write: {
            calls: 1,
            successful_calls: 0,
            failed_calls: 1,
            confirmed_bytes: 3,
            last_error: "broken pipe",
          },
          transport: {
            source: "client",
            stage: "post_payload_visible",
            kind: "disconnect",
            signal: "client_write_error",
          },
        })}
      />,
    );
    expect(
      within(
        screen.getByRole("region", { name: "Upstream Completion" }),
      ).getByText("response.completed"),
    ).toBeInTheDocument();
    const writes = screen.getByRole("region", { name: "Downstream Writes" });
    expect(within(writes).getByText("broken pipe")).toBeInTheDocument();
    expect(within(writes).getByText("3")).toBeInTheDocument();
    expect(
      screen.getByRole("region", { name: "Transport" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/abandoned|fully received/i),
    ).not.toBeInTheDocument();
  });

  it("keeps an unfinished current round separate from the previous completed response", () => {
    render(
      <RequestEvidenceViewer
        evidenceJson={JSON.stringify({
          v: 2,
          upstream_responses: {
            current: { round: 2, observed_at: "2026-09-08T11:01:00Z" },
            last_completed: {
              round: 1,
              response_id: "previous-response",
              event_type: "response.completed",
              status: "completed",
            },
            completed_responses: 1,
          },
        })}
      />,
    );
    expect(screen.getByText("Current round")).toBeInTheDocument();
    expect(screen.getByText("No upstream event observed")).toBeInTheDocument();
    expect(screen.getByText("Last completed response")).toBeInTheDocument();
    expect(screen.getByText("previous-response")).toBeInTheDocument();
  });

  it("renders successful writes without inventing an upstream completion", () => {
    render(
      <RequestEvidenceViewer
        evidenceJson={JSON.stringify({
          v: 2,
          downstream_write: {
            calls: 2,
            successful_calls: 2,
            failed_calls: 0,
            confirmed_bytes: 0,
          },
        })}
      />,
    );
    expect(
      screen.getByRole("region", { name: "Downstream Writes" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("region", { name: "Upstream Completion" }),
    ).not.toBeInTheDocument();
  });
});
