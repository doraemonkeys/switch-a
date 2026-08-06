import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ResponseBodyParser } from "./ResponseBodyParser";

describe("ResponseBodyParser", () => {
  it("renders structured object errors and an accessible raw view", () => {
    render(
      <ResponseBodyParser
        body={JSON.stringify({
          error: {
            type: "invalid_request_error",
            message: "model is unavailable",
          },
        })}
        statusCode={400}
        tone="error"
      />,
    );

    expect(screen.getByText("invalid_request_error")).toBeInTheDocument();
    expect(screen.getByText("model is unavailable")).toBeInTheDocument();
    expect(
      screen.getByRole("group", { name: "Raw response" }),
    ).toBeInTheDocument();
  });

  it("keeps valid JSON arrays on a safe structured path", () => {
    render(
      <ResponseBodyParser
        body='["first","second"]'
        statusCode={502}
        tone="error"
      />,
    );

    expect(screen.getByText("value:")).toBeInTheDocument();
    expect(screen.getByText('["first","second"]')).toBeInTheDocument();
  });

  it("falls back to raw text for non-JSON bodies", () => {
    render(
      <ResponseBodyParser
        body="upstream disconnected"
        statusCode={502}
        tone="error"
      />,
    );

    expect(screen.getByText("upstream disconnected")).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Raw response" })).toBeNull();
  });

  it("does not render empty bodies", () => {
    const { container } = render(
      <ResponseBodyParser body="  " statusCode={500} tone="error" />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("renders non-error bodies without the error frame", () => {
    const { container } = render(
      <ResponseBodyParser
        body={"event: response.created\ndata: {}"}
        statusCode={200}
        tone="neutral"
      />,
    );

    expect(screen.getByText(/event: response\.created/)).toBeInTheDocument();
    expect(container.querySelector(".bg-red-50\\/50")).not.toBeInTheDocument();
  });
});
