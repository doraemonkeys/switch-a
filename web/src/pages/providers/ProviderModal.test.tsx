import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ProviderModal } from "./ProviderModal";

describe("ProviderModal", () => {
  it("submits when an API type provides its own key override", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();

    render(<ProviderModal onClose={onClose} onSubmit={onSubmit} groups={[]} />);

    await user.type(screen.getByLabelText("Name"), "Split Credentials");
    await user.click(screen.getByRole("button", { name: "claude" }));
    await user.type(
      screen.getByLabelText("Base URL for claude"),
      "https://api.example.com",
    );
    await user.type(
      screen.getByLabelText("API key override for claude"),
      "claude-key",
    );

    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          id: expect.stringMatching(/^split-credentials/),
          name: "Split Credentials",
          api_key: "",
          api_types: [
            {
              api_type: "claude",
              base_url: "https://api.example.com",
              api_key: "claude-key",
            },
          ],
        }),
      ),
    );
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("shows an error when neither the default key nor an override is provided", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(<ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />);

    await user.type(screen.getByLabelText("Name"), "Missing Key");
    await user.click(screen.getByRole("button", { name: "claude" }));
    await user.type(
      screen.getByLabelText("Base URL for claude"),
      "https://api.example.com",
    );

    await user.click(screen.getByRole("button", { name: /add provider/i }));

    expect(
      await screen.findByText(/api key is required for api type "claude"/i),
    ).toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("normalizes whitespace-only overrides before submit", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(<ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />);

    await user.type(screen.getByLabelText("Name"), "Whitespace Override");
    await user.type(
      screen.getByLabelText("Default API Key"),
      "  default-key  ",
    );
    await user.click(screen.getByRole("button", { name: "claude" }));
    await user.type(
      screen.getByLabelText("Base URL for claude"),
      "https://api.example.com",
    );
    await user.type(
      screen.getByLabelText("API key override for claude"),
      "   ",
    );

    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          api_key: "default-key",
          api_types: [
            expect.objectContaining({
              api_type: "claude",
              api_key: "",
            }),
          ],
        }),
      ),
    );
  });

  it("toggles visibility for an API key override", async () => {
    const user = userEvent.setup();

    render(<ProviderModal onClose={vi.fn()} onSubmit={vi.fn()} groups={[]} />);

    await user.type(screen.getByLabelText("Name"), "Visible Override");
    await user.click(screen.getByRole("button", { name: "claude" }));

    const overrideInput = screen.getByLabelText("API key override for claude");
    expect(overrideInput).toHaveAttribute("type", "password");

    await user.click(
      screen.getByRole("button", {
        name: "Show API key override for claude",
      }),
    );

    expect(overrideInput).toHaveAttribute("type", "text");
  });
});
