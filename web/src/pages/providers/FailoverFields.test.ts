import { createElement } from "react";
import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { FAILOVER_SCOPES } from "../../config/constants";
import { FailoverScopeField } from "./FailoverFields";
import { hasFailoverConfig } from "./failoverConfig";
import type { ProviderFormData } from "./types";

const BASE_INPUT: ProviderFormData = {
  id: "test",
  name: "Test",
  credential_mode: "api_key",
  new_shared_api_key: "key",
  api_types: [],
  vendor: "",
  failover_scope: FAILOVER_SCOPES.ANY,
  accept_failover: FAILOVER_SCOPES.ANY,
  enabled: true,
};

describe("hasFailoverConfig", () => {
  it("returns false when all failover fields are defaults", () => {
    expect(hasFailoverConfig(BASE_INPUT)).toBe(false);
  });

  it("returns true when vendor is set", () => {
    expect(hasFailoverConfig({ ...BASE_INPUT, vendor: "openai" })).toBe(true);
  });

  it("returns false when failover_scope is 'any' (default)", () => {
    expect(
      hasFailoverConfig({
        ...BASE_INPUT,
        failover_scope: FAILOVER_SCOPES.ANY,
      }),
    ).toBe(false);
  });

  it("returns true when failover_scope is 'vendor'", () => {
    expect(
      hasFailoverConfig({
        ...BASE_INPUT,
        failover_scope: FAILOVER_SCOPES.VENDOR,
      }),
    ).toBe(true);
  });

  it("returns true when failover_scope is 'none'", () => {
    expect(
      hasFailoverConfig({
        ...BASE_INPUT,
        failover_scope: FAILOVER_SCOPES.NONE,
      }),
    ).toBe(true);
  });

  it("returns true when accept_failover is 'none'", () => {
    expect(
      hasFailoverConfig({
        ...BASE_INPUT,
        accept_failover: FAILOVER_SCOPES.NONE,
      }),
    ).toBe(true);
  });

  it("returns true when accept_failover is 'vendor'", () => {
    expect(
      hasFailoverConfig({
        ...BASE_INPUT,
        accept_failover: FAILOVER_SCOPES.VENDOR,
      }),
    ).toBe(true);
  });

  it("returns true when vendor and non-default scope are both set", () => {
    expect(
      hasFailoverConfig({
        ...BASE_INPUT,
        vendor: "openai",
        failover_scope: FAILOVER_SCOPES.VENDOR,
        accept_failover: FAILOVER_SCOPES.NONE,
      }),
    ).toBe(true);
  });

  it("returns false when failover_scope and accept_failover are both undefined", () => {
    expect(
      hasFailoverConfig({
        ...BASE_INPUT,
        failover_scope: undefined as never,
        accept_failover: undefined as never,
      }),
    ).toBe(false);
  });

  it("explains that inbound accept failover does not block pre-visible replacement", () => {
    render(
      createElement(FailoverScopeField, {
        label: "Accept Failover (Inbound)",
        value: FAILOVER_SCOPES.ANY,
        onChange: vi.fn(),
        direction: "inbound",
      }),
    );

    expect(
      screen.getByText(/Pre-visible provider replacement is unaffected/i),
    ).toBeInTheDocument();
  });
});
