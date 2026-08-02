import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { DebugCaptureFailureObservation } from "@/api";
import { CaptureFailureContext } from "./CaptureFailureContext";

const emptyFailure: DebugCaptureFailureObservation = {
  primary: {
    site: "unknown",
    peer: "unknown",
    class: "unknown",
    code: "unknown",
  },
  secondary: {
    site: "unknown",
    peer: "unknown",
    class: "unknown",
    code: "unknown",
  },
  has_secondary: false,
  truncated: false,
};

describe("CaptureFailureContext", () => {
  it("presents stable facts and bounded sanitized diagnostic metadata", () => {
    const unsafeMessage = `begin\u0000middle\u202e${"x".repeat(260)}tail`;
    render(
      <CaptureFailureContext
        label="Failure context"
        terminationReason="transport_error"
        hasFailure
        failure={{
          primary: {
            site: "response_status",
            peer: "provider",
            class: "http_status",
            code: "unexpected_status",
            http_status_code: 429,
            system_error_code: 12_345,
            provider_error_type: "model\u202e_error",
            provider_error_code: "model_not_allowed",
            message: unsafeMessage,
          },
          secondary: {
            site: "websocket_close",
            peer: "upstream",
            class: "websocket_close",
            code: "websocket_close",
            websocket_close_code: 1011,
          },
          has_secondary: true,
          truncated: true,
        }}
        metadataTruncated
      />,
    );

    const note = screen.getByRole("note", { name: "Failure context" });
    const primary = within(note).getByRole("region", {
      name: "Primary failure",
    });
    expect(primary).toHaveTextContent("Response Status");
    expect(primary).toHaveTextContent("Provider");
    expect(primary).toHaveTextContent("Http Status");
    expect(primary).toHaveTextContent("Unexpected Status");
    expect(primary).toHaveTextContent("429");
    expect(primary).toHaveTextContent("12345");
    expect(
      within(primary).getByText("Provider error type:"),
    ).toBeInTheDocument();
    expect(within(primary).getByText("model _error")).toBeInTheDocument();
    expect(
      within(primary).getByText("Provider error code:"),
    ).toBeInTheDocument();
    expect(within(primary).getByText("model_not_allowed")).toBeInTheDocument();
    expect(primary).toHaveTextContent("begin middle");
    expect(primary).toHaveTextContent("Message display truncated");
    expect(primary.textContent).not.toContain("\u202e");
    expect(primary.textContent).not.toContain("tail");

    const secondary = within(note).getByRole("region", {
      name: "Secondary failure",
    });
    expect(secondary).toHaveTextContent("Websocket Close");
    expect(secondary).toHaveTextContent("1011");
    expect(note).toHaveTextContent("Failure details truncated");
    expect(note).toHaveTextContent("Metadata truncated");
    expect(note).not.toHaveTextContent("Error:");
  });

  it("does not present a zero-value observation without a failure signal", () => {
    render(
      <CaptureFailureContext
        label="Failure context"
        hasFailure={false}
        failure={emptyFailure}
      />,
    );

    expect(
      screen.queryByRole("note", { name: "Failure context" }),
    ).not.toBeInTheDocument();
  });
});
