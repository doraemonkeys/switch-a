import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { ToastProvider } from "./Toast";
import { useToast } from "../hooks/useToast";

// Test component that uses the toast hook
function TestComponent() {
  const toast = useToast();

  return (
    <div>
      <button onClick={() => toast.success("Success message")}>
        Show Success
      </button>
      <button onClick={() => toast.error("Error message")}>Show Error</button>
      <button onClick={() => toast.warning("Warning message")}>
        Show Warning
      </button>
      <button onClick={() => toast.info("Info message")}>Show Info</button>
      <button
        onClick={() =>
          toast.addToast({ type: "info", message: "Custom", duration: 0 })
        }
      >
        Persistent Toast
      </button>
    </div>
  );
}

describe("Toast", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders success toast when success is called", () => {
    render(
      <ToastProvider>
        <TestComponent />
      </ToastProvider>,
    );

    fireEvent.click(screen.getByText("Show Success"));
    expect(screen.getByText("Success message")).toBeInTheDocument();
  });

  it("renders error toast when error is called", () => {
    render(
      <ToastProvider>
        <TestComponent />
      </ToastProvider>,
    );

    fireEvent.click(screen.getByText("Show Error"));
    expect(screen.getByText("Error message")).toBeInTheDocument();
  });

  it("renders warning toast when warning is called", () => {
    render(
      <ToastProvider>
        <TestComponent />
      </ToastProvider>,
    );

    fireEvent.click(screen.getByText("Show Warning"));
    expect(screen.getByText("Warning message")).toBeInTheDocument();
  });

  it("renders info toast when info is called", () => {
    render(
      <ToastProvider>
        <TestComponent />
      </ToastProvider>,
    );

    fireEvent.click(screen.getByText("Show Info"));
    expect(screen.getByText("Info message")).toBeInTheDocument();
  });

  it("auto-dismisses toast after default duration", () => {
    render(
      <ToastProvider>
        <TestComponent />
      </ToastProvider>,
    );

    fireEvent.click(screen.getByText("Show Success"));
    expect(screen.getByText("Success message")).toBeInTheDocument();

    // Fast forward past the default duration (4000ms) + exit animation (300ms)
    act(() => {
      vi.advanceTimersByTime(4300);
    });

    expect(screen.queryByText("Success message")).not.toBeInTheDocument();
  });

  it("does not auto-dismiss toast with duration 0", async () => {
    render(
      <ToastProvider>
        <TestComponent />
      </ToastProvider>,
    );

    fireEvent.click(screen.getByText("Persistent Toast"));
    expect(screen.getByText("Custom")).toBeInTheDocument();

    // Fast forward way past normal duration
    act(() => {
      vi.advanceTimersByTime(10000);
    });

    // Toast should still be there
    expect(screen.getByText("Custom")).toBeInTheDocument();
  });

  it("removes toast when close button is clicked", () => {
    render(
      <ToastProvider>
        <TestComponent />
      </ToastProvider>,
    );

    fireEvent.click(screen.getByText("Show Success"));
    expect(screen.getByText("Success message")).toBeInTheDocument();

    const closeButton = screen.getByLabelText("关闭通知");
    fireEvent.click(closeButton);

    // Wait for exit animation
    act(() => {
      vi.advanceTimersByTime(300);
    });

    expect(screen.queryByText("Success message")).not.toBeInTheDocument();
  });

  it("can display multiple toasts at once", () => {
    render(
      <ToastProvider>
        <TestComponent />
      </ToastProvider>,
    );

    fireEvent.click(screen.getByText("Show Success"));
    fireEvent.click(screen.getByText("Show Error"));
    fireEvent.click(screen.getByText("Show Warning"));

    expect(screen.getByText("Success message")).toBeInTheDocument();
    expect(screen.getByText("Error message")).toBeInTheDocument();
    expect(screen.getByText("Warning message")).toBeInTheDocument();
  });

  it("throws error when useToast is used outside provider", () => {
    // Suppress console.error for this test
    const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});

    function BrokenComponent() {
      useToast();
      return null;
    }

    expect(() => render(<BrokenComponent />)).toThrow(
      "useToast must be used within a ToastProvider",
    );

    consoleSpy.mockRestore();
  });
});
