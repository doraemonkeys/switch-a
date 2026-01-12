import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ErrorBoundary, ErrorFallback } from "./ErrorBoundary";

// Component that throws an error
function ThrowingComponent({ shouldThrow }: { shouldThrow: boolean }) {
  if (shouldThrow) {
    throw new Error("Test error message");
  }
  return <div>Normal content</div>;
}

describe("ErrorBoundary", () => {
  let consoleSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    // Suppress console.error for error boundary tests
    consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});
  });

  afterEach(() => {
    consoleSpy.mockRestore();
  });

  it("renders children when there is no error", () => {
    render(
      <ErrorBoundary>
        <div>Child content</div>
      </ErrorBoundary>,
    );

    expect(screen.getByText("Child content")).toBeInTheDocument();
  });

  it("renders default fallback UI when child throws an error", () => {
    render(
      <ErrorBoundary>
        <ThrowingComponent shouldThrow={true} />
      </ErrorBoundary>,
    );

    expect(screen.getByText("Something went wrong")).toBeInTheDocument();
    expect(screen.getByText("Test error message")).toBeInTheDocument();
  });

  it("renders custom fallback when provided", () => {
    render(
      <ErrorBoundary fallback={<div>Custom error fallback</div>}>
        <ThrowingComponent shouldThrow={true} />
      </ErrorBoundary>,
    );

    expect(screen.getByText("Custom error fallback")).toBeInTheDocument();
    expect(screen.queryByText("Something went wrong")).not.toBeInTheDocument();
  });

  it("logs error to console when error is caught", () => {
    render(
      <ErrorBoundary>
        <ThrowingComponent shouldThrow={true} />
      </ErrorBoundary>,
    );

    expect(consoleSpy).toHaveBeenCalledWith(
      "ErrorBoundary caught an error:",
      expect.any(Error),
      expect.any(Object),
    );
  });

  it("recovers when Try Again button is clicked", () => {
    let shouldThrow = true;

    // Use a wrapper that references the outer shouldThrow variable
    const ConditionalThrower = () => (
      <ThrowingComponent shouldThrow={shouldThrow} />
    );

    render(
      <ErrorBoundary>
        <ConditionalThrower />
      </ErrorBoundary>,
    );

    expect(screen.getByText("Something went wrong")).toBeInTheDocument();

    // Change the condition so component won't throw on next render
    shouldThrow = false;

    // Click Try Again - this resets error state and re-renders children
    fireEvent.click(screen.getByText("Try Again"));

    expect(screen.getByText("Normal content")).toBeInTheDocument();
  });

  it("shows the error description text", () => {
    render(
      <ErrorBoundary>
        <ThrowingComponent shouldThrow={true} />
      </ErrorBoundary>,
    );

    expect(
      screen.getByText(
        "An unexpected error occurred while rendering this page.",
      ),
    ).toBeInTheDocument();
  });
});

describe("ErrorFallback", () => {
  it("renders error message when error is provided", () => {
    const error = new Error("Custom error");

    render(<ErrorFallback error={error} />);

    expect(screen.getByText("Custom error")).toBeInTheDocument();
  });

  it("renders without error message when error is null", () => {
    render(<ErrorFallback error={null} />);

    expect(screen.getByText("Something went wrong")).toBeInTheDocument();
    // Error message section should not be rendered
    expect(screen.queryByRole("code")).not.toBeInTheDocument();
  });

  it("renders Try Again button when onRetry is provided", () => {
    const onRetry = vi.fn();

    render(<ErrorFallback error={null} onRetry={onRetry} />);

    expect(screen.getByText("Try Again")).toBeInTheDocument();
  });

  it("does not render Try Again button when onRetry is not provided", () => {
    render(<ErrorFallback error={null} />);

    expect(screen.queryByText("Try Again")).not.toBeInTheDocument();
  });

  it("calls onRetry when Try Again button is clicked", () => {
    const onRetry = vi.fn();

    render(<ErrorFallback error={null} onRetry={onRetry} />);

    fireEvent.click(screen.getByText("Try Again"));

    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("displays the warning emoji", () => {
    render(<ErrorFallback error={null} />);

    expect(screen.getByText("⚠️")).toBeInTheDocument();
  });

  it("renders title correctly", () => {
    render(<ErrorFallback error={null} />);

    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent(
      "Something went wrong",
    );
  });
});
