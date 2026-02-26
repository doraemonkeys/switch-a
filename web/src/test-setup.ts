import "@testing-library/jest-dom";
import { afterEach } from "vitest";

/**
 * Test setup for Vitest + React Testing Library
 *
 * Following Engineering Guidelines:
 * - Accessibility-First Testing: getByRole, getByLabelText over data-testid
 * - Logic-View Separation: Test hooks and logic separately from components
 */

// Promote React act() warnings to hard failures so they surface in CI
// instead of silently hiding async regressions.
// We collect rather than throw inline to avoid corrupting React's fiber tree
// during commit, which would break findBy/waitFor queries.
const actWarnings: string[] = [];
const originalConsoleError = console.error;
console.error = (...args: unknown[]) => {
  originalConsoleError(...args);
  const message = typeof args[0] === "string" ? args[0] : "";
  if (message.includes("not wrapped in act(")) {
    actWarnings.push(message);
  }
};

afterEach(() => {
  const warnings = actWarnings.splice(0);
  if (warnings.length > 0) {
    throw new Error(
      `${warnings.length} React act() warning(s) — use findBy/waitFor or act(async ...)`,
    );
  }
});
