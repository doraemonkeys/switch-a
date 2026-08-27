import { describe, expectTypeOf, it } from "vitest";
import type * as Direct from "./config-transfer-types";
import type * as Legacy from "./types";
import type * as Public from ".";

type DirectSurface = [
  Direct.ExportedAPIType,
  Direct.ExportedCredentialSession,
  Direct.ExportedCredentialSubject,
  Direct.ExportedCredentialAuthState,
  Direct.ExportedCredentialUsageSnapshot,
  Direct.ExportedCredentialUsageWindow,
  Direct.CredentialSessionKind,
  Direct.CredentialSessionTransferMode,
  Direct.ExportedProvider,
  Direct.ExportedGroup,
  Direct.ExportedRoutingPolicy,
  Direct.ExportedInternalErrorRule,
  Direct.ExportedConfig,
  Direct.ImportMode,
  Direct.FullImportScope,
  Direct.SettingsOnlyImportScope,
  Direct.SelectionImportScope,
  Direct.ImportScope,
  Direct.ImportConfigRequest,
  Direct.ChangeCount,
  Direct.ImportChanges,
  Direct.ImportPreviewResponse,
  Direct.AppliedCount,
  Direct.ImportedCounts,
  Direct.ImportResult,
];

type LegacySurface = [
  Legacy.ExportedAPIType,
  Legacy.ExportedCredentialSession,
  Legacy.ExportedCredentialSubject,
  Legacy.ExportedCredentialAuthState,
  Legacy.ExportedCredentialUsageSnapshot,
  Legacy.ExportedCredentialUsageWindow,
  Legacy.CredentialSessionKind,
  Legacy.CredentialSessionTransferMode,
  Legacy.ExportedProvider,
  Legacy.ExportedGroup,
  Legacy.ExportedRoutingPolicy,
  Legacy.ExportedInternalErrorRule,
  Legacy.ExportedConfig,
  Legacy.ImportMode,
  Legacy.FullImportScope,
  Legacy.SettingsOnlyImportScope,
  Legacy.SelectionImportScope,
  Legacy.ImportScope,
  Legacy.ImportConfigRequest,
  Legacy.ChangeCount,
  Legacy.ImportChanges,
  Legacy.ImportPreviewResponse,
  Legacy.AppliedCount,
  Legacy.ImportedCounts,
  Legacy.ImportResult,
];

type PublicSurface = [
  Public.ExportedAPIType,
  Public.ExportedCredentialSession,
  Public.ExportedCredentialSubject,
  Public.ExportedCredentialAuthState,
  Public.ExportedCredentialUsageSnapshot,
  Public.ExportedCredentialUsageWindow,
  Public.CredentialSessionKind,
  Public.CredentialSessionTransferMode,
  Public.ExportedProvider,
  Public.ExportedGroup,
  Public.ExportedRoutingPolicy,
  Public.ExportedInternalErrorRule,
  Public.ExportedConfig,
  Public.ImportMode,
  Public.FullImportScope,
  Public.SettingsOnlyImportScope,
  Public.SelectionImportScope,
  Public.ImportScope,
  Public.ImportConfigRequest,
  Public.ChangeCount,
  Public.ImportChanges,
  Public.ImportPreviewResponse,
  Public.AppliedCount,
  Public.ImportedCounts,
  Public.ImportResult,
];

describe("config-transfer type surface", () => {
  it("preserves the legacy and public API re-exports", () => {
    expectTypeOf<LegacySurface>().toEqualTypeOf<DirectSurface>();
    expectTypeOf<PublicSurface>().toEqualTypeOf<DirectSurface>();
  });
});
