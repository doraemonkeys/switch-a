import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ProviderImportCommitResult } from "../../api";
import { ResultStep } from "./ResultStep";

const result: ProviderImportCommitResult = {
  import_id: "import-1",
  summary: { created: 1, updated: 1, skipped: 1 },
  items: [
    {
      candidate_id: "created-1",
      outcome: "created",
      provider_id: "created-provider",
      name: "Created Provider",
    },
    {
      candidate_id: "updated-1",
      outcome: "updated",
      provider_id: "updated-provider",
    },
  ],
};

describe("ResultStep", () => {
  it("lists only applied mutations while keeping skipped accounts aggregate-only", () => {
    render(<ResultStep result={result} />);

    expect(
      screen.getByRole("heading", { name: "Import complete" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Provider results" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Accounts imported")).not.toBeInTheDocument();
    expect(screen.queryByText("Imported providers")).not.toBeInTheDocument();

    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveTextContent("Created Provider");
    expect(items[0]).toHaveTextContent("created");
    expect(items[1]).toHaveTextContent("updated-provider");
    expect(items[1]).toHaveTextContent("updated");
    expect(screen.queryByText(/not selected/i)).not.toBeInTheDocument();
  });

  it("announces only the concise aggregate result", () => {
    render(<ResultStep result={result} />);

    const status = screen.getByRole("status");
    expect(status).toHaveTextContent(
      "Import complete. 1 created, 1 updated, 1 skipped.",
    );
    expect(status).not.toHaveTextContent("Created Provider");
    expect(status).not.toHaveTextContent("updated-provider");

    const results = screen.getByRole("list");
    expect(results.closest("[aria-live]")).toBeNull();
    const summary = screen.getByLabelText("Import result summary");
    expect(summary.querySelectorAll("dt")).toHaveLength(3);
    expect(summary.querySelectorAll("dd")).toHaveLength(3);
  });
});
