import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ErrorBodyParser } from "./ErrorBodyParser";

describe("ErrorBodyParser", () => {
  it("renders structured object errors and an accessible raw view", () => {
    render(
      <ErrorBodyParser
        body={JSON.stringify({
          error: {
            type: "invalid_request_error",
            message: "model is unavailable",
          },
        })}
        statusCode={400}
      />,
    );

    expect(screen.getByText("invalid_request_error")).toBeInTheDocument();
    expect(screen.getByText("model is unavailable")).toBeInTheDocument();
    expect(
      screen.getByRole("group", { name: "Raw response" }),
    ).toBeInTheDocument();
  });

  it("keeps valid JSON arrays on a safe structured path", () => {
    render(<ErrorBodyParser body='["first","second"]' statusCode={502} />);

    expect(screen.getByText("value:")).toBeInTheDocument();
    expect(screen.getByText('["first","second"]')).toBeInTheDocument();
  });

  it("falls back to raw text for non-JSON bodies", () => {
    render(<ErrorBodyParser body="upstream disconnected" statusCode={502} />);

    expect(screen.getByText("upstream disconnected")).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Raw response" })).toBeNull();
  });

  it("does not render empty bodies", () => {
    const { container } = render(
      <ErrorBodyParser body="  " statusCode={500} />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
