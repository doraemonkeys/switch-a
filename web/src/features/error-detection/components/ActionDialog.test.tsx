import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ActionDialog } from "./ActionDialog";

function DialogHarness() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open confirmation
      </button>
      <ActionDialog
        open={open}
        title="Replace draft?"
        description="Unsaved fields will be replaced."
        confirmLabel="Replace"
        onConfirm={() => setOpen(false)}
        onCancel={() => setOpen(false)}
      />
    </>
  );
}

describe("ActionDialog", () => {
  it("traps keyboard focus, closes on Escape, and restores the invoker", async () => {
    const user = userEvent.setup();
    render(<DialogHarness />);
    const invoker = screen.getByRole("button", { name: "Open confirmation" });

    await user.click(invoker);

    const dialog = screen.getByRole("alertdialog", { name: "Replace draft?" });
    const cancel = screen.getByRole("button", { name: "Cancel" });
    const confirm = screen.getByRole("button", { name: "Replace" });
    expect(dialog).toHaveAttribute("aria-modal", "true");
    expect(cancel).toHaveFocus();

    await user.tab({ shift: true });
    expect(confirm).toHaveFocus();
    await user.tab();
    expect(cancel).toHaveFocus();

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(invoker).toHaveFocus();
  });
});
