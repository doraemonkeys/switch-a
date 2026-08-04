import { Link } from "react-router";
import {
  findBuiltInAPIType,
  isValidCustomAPIType,
  useAPICatalog,
  useApi,
  type APICatalog,
} from "@/api";
import type { Provider } from "@/api/types";
import type { InternalErrorRule } from "@/features/error-detection/contracts";
import { useQuery } from "@/hooks/useQuery";
import { DetailSection } from "./DrawerSection";

const ERROR_DETECTION_ROUTE = "/error-detection";

interface APITypeCapability {
  readonly apiType: string;
  readonly label: string;
  readonly supported: boolean;
  readonly unavailableReason: string | null;
}

function buildProviderErrorDetectionPath(
  providerID: string,
  apiType: string,
): string {
  const query = new URLSearchParams({
    scope: "provider",
    provider_id: providerID,
    api_type: apiType,
  });
  return `${ERROR_DETECTION_ROUTE}?${query.toString()}`;
}

function getProviderAPITypeCapabilities(
  provider: Provider,
  catalog: APICatalog,
): readonly APITypeCapability[] {
  const seen = new Set<string>();
  const capabilities: APITypeCapability[] = [];

  for (const { api_type: apiType } of provider.api_types ?? []) {
    if (seen.has(apiType)) continue;
    seen.add(apiType);

    const catalogEntry = findBuiltInAPIType(catalog, apiType);
    if (catalogEntry?.semantic_error_supported) {
      capabilities.push({
        apiType,
        label: catalogEntry.label,
        supported: true,
        unavailableReason: null,
      });
      continue;
    }

    if (isValidCustomAPIType(catalog, apiType)) {
      capabilities.push({
        apiType,
        label: apiType,
        supported: false,
        unavailableReason:
          "Custom API types do not expose a structured response protocol.",
      });
      continue;
    }

    capabilities.push({
      apiType,
      label: catalogEntry?.label ?? apiType,
      supported: false,
      unavailableReason: catalogEntry
        ? "The server catalog marks structured error detection unsupported."
        : "This API type is absent from the current server catalog.",
    });
  }

  return capabilities;
}

function selectEffectiveRules(
  rules: readonly InternalErrorRule[],
  providerID: string,
  supportedAPITypes: ReadonlySet<string>,
): readonly InternalErrorRule[] {
  return rules.filter((rule) => {
    if (!rule.enabled) return false;
    if (
      rule.target.kind === "provider" &&
      rule.target.provider_id !== providerID
    ) {
      return false;
    }

    // A null API scope is effective only when this provider has at least one
    // catalog-supported protocol; custom transports never gain semantics by fallback.
    return rule.api_type === null
      ? supportedAPITypes.size > 0
      : supportedAPITypes.has(rule.api_type);
  });
}

function actionLabel(rule: InternalErrorRule): string {
  if (rule.action.type === "passthrough") return "Pass through";
  if (rule.action.type === "retry_only") {
    return `Retry same provider up to ${rule.action.max_retries}×`;
  }
  return `Retry up to ${rule.action.max_retries}×, then switch`;
}

function ruleAPITypeLabel(
  rule: InternalErrorRule,
  catalog: APICatalog,
): string {
  if (rule.api_type === null) return "All supported APIs";
  return findBuiltInAPIType(catalog, rule.api_type)?.label ?? rule.api_type;
}

function RuleList({
  catalog,
  providerID,
  rules,
  supportedAPITypes,
}: {
  readonly catalog: APICatalog;
  readonly providerID: string;
  readonly rules: readonly InternalErrorRule[];
  readonly supportedAPITypes: ReadonlySet<string>;
}) {
  const effectiveRules = selectEffectiveRules(
    rules,
    providerID,
    supportedAPITypes,
  );

  if (effectiveRules.length === 0) {
    return (
      <p className="rounded-lg bg-bg-tertiary/50 px-3 py-2 text-xs text-text-muted">
        No enabled global or provider rules currently apply.
      </p>
    );
  }

  return (
    <ul aria-label="Effective internal error rules" className="space-y-2">
      {effectiveRules.map((rule) => (
        <li
          key={rule.id}
          className="rounded-lg border border-border-light bg-bg-tertiary/40 px-3 py-2"
        >
          <div className="flex items-start justify-between gap-3">
            <span className="text-sm font-medium text-text-primary">
              {rule.name}
            </span>
            <span className="shrink-0 rounded bg-bg-tertiary px-1.5 py-0.5 text-[10px] uppercase text-text-muted">
              {rule.target.kind === "global" ? "Global" : "Provider"}
            </span>
          </div>
          <p className="mt-1 text-xs text-text-secondary">
            {ruleAPITypeLabel(rule, catalog)} · {actionLabel(rule)}
          </p>
        </li>
      ))}
    </ul>
  );
}

function APITypeLinks({
  capabilities,
  provider,
}: {
  readonly capabilities: readonly APITypeCapability[];
  readonly provider: Provider;
}) {
  if (capabilities.length === 0) {
    return (
      <p className="text-xs text-text-muted">
        Configure an API type before creating provider-scoped rules.
      </p>
    );
  }

  return (
    <ul aria-label="Rule management by API type" className="space-y-2">
      {capabilities.map((capability) => (
        <li
          key={capability.apiType}
          className="flex items-start justify-between gap-3 rounded-lg border border-border-light px-3 py-2"
        >
          <div className="min-w-0">
            <p className="truncate text-sm font-medium text-text-primary">
              {capability.label}
            </p>
            <p className="truncate font-mono text-[11px] text-text-muted">
              {capability.apiType}
            </p>
          </div>
          {capability.supported ? (
            <Link
              to={buildProviderErrorDetectionPath(
                provider.id,
                capability.apiType,
              )}
              className="shrink-0 text-xs font-medium text-primary hover:text-primary-hover"
              aria-label={`Manage ${capability.label} error detection rules for ${provider.name}`}
            >
              Manage rules →
            </Link>
          ) : (
            <span
              className="max-w-44 text-right text-xs text-warning-dark"
              aria-label={`${capability.label}: structured error detection unavailable`}
              title={capability.unavailableReason ?? undefined}
            >
              Structured error detection unavailable
            </span>
          )}
        </li>
      ))}
    </ul>
  );
}

export function ProviderErrorDetectionSummary({
  provider,
}: {
  readonly provider: Provider;
}) {
  const api = useApi();
  const catalogState = useAPICatalog();
  const rulesQuery = useQuery(() => api.errorDetection.rules.list(), {
    queryKey: api.errorDetection.rules,
    errorMessage: "Failed to load internal error rules",
  });
  const catalog = catalogState.catalog;

  if (!catalog) {
    return (
      <DetailSection title="Internal Error Detection">
        <div
          role={catalogState.error ? "alert" : "status"}
          className="rounded-lg bg-bg-tertiary/50 px-3 py-2 text-xs text-text-secondary"
        >
          {catalogState.error
            ? `Rule links are unavailable: ${catalogState.error.message}`
            : "Loading API support from the server catalog…"}
        </div>
      </DetailSection>
    );
  }

  const capabilities = getProviderAPITypeCapabilities(provider, catalog);
  const supportedAPITypes = new Set(
    capabilities
      .filter((capability) => capability.supported)
      .map((capability) => capability.apiType),
  );

  return (
    <DetailSection title="Internal Error Detection">
      <p className="text-xs text-text-muted">
        Read-only summary. Rules can only be changed on the Error Detection
        page.
      </p>

      {rulesQuery.loading && (
        <p role="status" className="text-xs text-text-secondary">
          Loading effective rules…
        </p>
      )}
      {rulesQuery.error && (
        <p role="alert" className="text-xs text-danger">
          Rule summary unavailable: {rulesQuery.error.message}
        </p>
      )}
      {rulesQuery.data && (
        <RuleList
          catalog={catalog}
          providerID={provider.id}
          rules={rulesQuery.data.value.rules}
          supportedAPITypes={supportedAPITypes}
        />
      )}

      <div className="pt-1">
        <h4 className="mb-2 text-xs font-semibold text-text-secondary">
          Manage by configured API type
        </h4>
        <APITypeLinks capabilities={capabilities} provider={provider} />
      </div>
    </DetailSection>
  );
}
