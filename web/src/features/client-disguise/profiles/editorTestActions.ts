import { screen, within } from "@testing-library/react";
import type userEvent from "@testing-library/user-event";
import { expect } from "vitest";

export async function chooseSnapshot(
  user: ReturnType<typeof userEvent.setup>,
  id: string,
) {
  await user.click(screen.getByRole("radio", { name: /固定快照/ }));
  const group = screen.getByRole("group", { name: "环境快照" });
  const find = () =>
    within(group)
      .getAllByRole("radio")
      .find((radio) => radio.getAttribute("value") === id);
  if (!find()) {
    for (const button of within(group).queryAllByRole("button", {
      name: /查看同版本历史/,
    }))
      await user.click(button);
  }
  const radio = find();
  if (!radio) throw new Error("Snapshot option missing: " + id);
  await user.click(radio);
}
export function expectSnapshot(id: string) {
  expect(
    within(screen.getByLabelText("生效预览")).getByLabelText("快照 ID"),
  ).toHaveTextContent(id);
}
