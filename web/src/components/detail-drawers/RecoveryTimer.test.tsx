import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { RecoveryTimer } from "./RecoveryTimer";

describe("RecoveryTimer", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("When time has not expired", () => {
    it("shows countdown in 'Xm Ys' format", () => {
      const futureTime = new Date(Date.now() + 5 * 60 * 1000 + 30 * 1000); // 5m 30s from now
      render(<RecoveryTimer disabledUntil={futureTime.toISOString()} />);

      expect(screen.getByText(/5m 30s/)).toBeInTheDocument();
    });

    it("shows timer emoji", () => {
      const futureTime = new Date(Date.now() + 60 * 1000); // 1 minute from now
      render(<RecoveryTimer disabledUntil={futureTime.toISOString()} />);

      expect(screen.getByText(/⏱️/)).toBeInTheDocument();
    });

    it("updates countdown every second", () => {
      const futureTime = new Date(Date.now() + 2 * 60 * 1000); // 2 minutes from now
      render(<RecoveryTimer disabledUntil={futureTime.toISOString()} />);

      // Shows initial countdown (approximately 2 minutes)
      expect(screen.getByText(/2m 0s|1m 59s/)).toBeInTheDocument();

      act(() => {
        vi.advanceTimersByTime(1000);
      });

      // After 1 second, time decreases
      expect(screen.getByText(/1m 59s|1m 58s/)).toBeInTheDocument();
    });

    it("applies custom className", () => {
      const futureTime = new Date(Date.now() + 60 * 1000);
      render(
        <RecoveryTimer
          disabledUntil={futureTime.toISOString()}
          className="custom-class"
        />,
      );

      const timer = screen.getByText(/⏱️/).closest("span");
      expect(timer).toHaveClass("custom-class");
    });
  });

  describe("When time has expired", () => {
    it("shows dash when showExpired is false (default)", () => {
      const pastTime = new Date(Date.now() - 60 * 1000); // 1 minute ago
      render(<RecoveryTimer disabledUntil={pastTime.toISOString()} />);

      expect(screen.getByText("—")).toBeInTheDocument();
    });

    it("shows 'Expired' when showExpired is true", () => {
      const pastTime = new Date(Date.now() - 60 * 1000); // 1 minute ago
      render(
        <RecoveryTimer
          disabledUntil={pastTime.toISOString()}
          showExpired={true}
        />,
      );

      expect(screen.getByText(/Expired/)).toBeInTheDocument();
    });

    it("clears interval when time expires", () => {
      const futureTime = new Date(Date.now() + 2000); // 2 seconds from now
      render(<RecoveryTimer disabledUntil={futureTime.toISOString()} />);

      // Initially shows countdown (approximately 0m 1s or 0m 2s)
      expect(screen.getByText(/0m [12]s/)).toBeInTheDocument();

      // Advance past expiry
      act(() => {
        vi.advanceTimersByTime(3000);
      });

      // Should show dash
      expect(screen.getByText("—")).toBeInTheDocument();
    });

    it("shows Expired and clears interval when showExpired is true", () => {
      const futureTime = new Date(Date.now() + 1000); // 1 second from now
      render(
        <RecoveryTimer
          disabledUntil={futureTime.toISOString()}
          showExpired={true}
        />,
      );

      // Advance past expiry
      act(() => {
        vi.advanceTimersByTime(2000);
      });

      expect(screen.getByText(/Expired/)).toBeInTheDocument();
    });
  });

  describe("Cleanup on unmount", () => {
    it("clears interval on unmount", () => {
      const clearIntervalSpy = vi.spyOn(global, "clearInterval");
      const futureTime = new Date(Date.now() + 60 * 1000);

      const { unmount } = render(
        <RecoveryTimer disabledUntil={futureTime.toISOString()} />,
      );

      unmount();

      expect(clearIntervalSpy).toHaveBeenCalled();
      clearIntervalSpy.mockRestore();
    });
  });

  describe("Styling", () => {
    it("has warning styling classes", () => {
      const futureTime = new Date(Date.now() + 60 * 1000);
      render(<RecoveryTimer disabledUntil={futureTime.toISOString()} />);

      const timer = screen.getByText(/⏱️/).closest("span");
      expect(timer).toHaveClass("bg-warning-light");
      expect(timer).toHaveClass("text-warning-dark");
    });

    it("has monospace font", () => {
      const futureTime = new Date(Date.now() + 60 * 1000);
      render(<RecoveryTimer disabledUntil={futureTime.toISOString()} />);

      const timer = screen.getByText(/⏱️/).closest("span");
      expect(timer).toHaveClass("font-mono");
    });
  });
});
