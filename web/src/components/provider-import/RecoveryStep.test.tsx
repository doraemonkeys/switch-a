import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { RecoveryStep } from "./RecoveryStep";

describe("RecoveryStep", () => {
  it("treats a commit mismatch as a potentially applied selection", () => {
    render(<RecoveryStep reason="committed_mismatch" />);

    expect(
      screen.getByRole("heading", {
        name: "A different selection was already committed",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Check Providers for any applied changes/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/did not import/i)).not.toBeInTheDocument();
  });
});
