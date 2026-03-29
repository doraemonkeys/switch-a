import type {
  Provider,
  RoutingPolicy,
  RoutingPolicyInput,
  RoutingPolicyModelMatchType,
} from "../../api";

export const EMPTY_MODEL_MATCH = "";
export const EMPTY_PROVIDER = "";
export const TARGET_MODE_EXACT_PROVIDER = "exact-provider";
export const TARGET_MODE_FILTERS = "group-vendor-filters";

export type RoutingPolicyTargetMode =
  | typeof TARGET_MODE_EXACT_PROVIDER
  | typeof TARGET_MODE_FILTERS;

export const MODEL_MATCH_OPTIONS: Array<{
  value: RoutingPolicyModelMatchType | typeof EMPTY_MODEL_MATCH;
  label: string;
  hint: string;
}> = [
  {
    value: EMPTY_MODEL_MATCH,
    label: "Any model",
    hint: "Match only on api_type when the model is unknown or unrestricted.",
  },
  {
    value: "exact",
    label: "Exact",
    hint: "Apply the rule only when the request model matches exactly.",
  },
  {
    value: "prefix",
    label: "Prefix",
    hint: "Apply the rule when the request model starts with the configured value.",
  },
];

export const TARGET_MODE_OPTIONS: Array<{
  value: RoutingPolicyTargetMode;
  label: string;
  hint: string;
}> = [
  {
    value: TARGET_MODE_EXACT_PROVIDER,
    label: "Exact provider",
    hint: "Constrain the rule to one provider ID. Group and vendor filters do not participate in this mode.",
  },
  {
    value: TARGET_MODE_FILTERS,
    label: "Group/vendor filters",
    hint: "Intersect allowed groups and provider-derived vendors. At least one filter must be selected.",
  },
];

export const STALE_VENDOR_VALIDATION_MESSAGE =
  "Remove stale vendors before changing vendor filters. Only the unchanged stored vendor set can survive catalog drift.";
export const STALE_VENDOR_HELP_MESSAGE =
  "Persisted stale vendors stay visible until you remove them, so the UI never mutates scope silently.";

export interface RoutingPolicyDraft {
  api_type: string;
  enabled: boolean;
  model_match_type: RoutingPolicyModelMatchType | typeof EMPTY_MODEL_MATCH;
  model_match_value: string;
  target_mode: RoutingPolicyTargetMode;
  target_provider_id: string;
  allowed_group_ids: string[];
  allowed_vendors: string[];
}

export function createEmptyDraft(): RoutingPolicyDraft {
  return {
    api_type: "",
    enabled: true,
    model_match_type: EMPTY_MODEL_MATCH,
    model_match_value: "",
    target_mode: TARGET_MODE_FILTERS,
    target_provider_id: EMPTY_PROVIDER,
    allowed_group_ids: [],
    allowed_vendors: [],
  };
}

export function normalizeStringList(values: string[]): string[] {
  return Array.from(
    new Set(
      values.map((value) => value.trim()).filter((value) => value.length > 0),
    ),
  );
}

export function supportsApiType(provider: Provider, apiType: string): boolean {
  const normalizedAPIType = apiType.trim();
  if (!normalizedAPIType) {
    return false;
  }

  return provider.api_types.some(
    (apiTypeConfig) => apiTypeConfig.api_type === normalizedAPIType,
  );
}

export function getCompatibleProviders(
  providers: Provider[],
  apiType: string,
): Provider[] {
  return providers
    .filter((provider) => supportsApiType(provider, apiType))
    .slice()
    .sort((left, right) => left.name.localeCompare(right.name));
}

export function getProviderDerivedVendors(
  providers: Provider[],
  apiType: string,
): string[] {
  return Array.from(
    new Set(
      getCompatibleProviders(providers, apiType)
        .map((provider) => provider.vendor.trim())
        .filter((vendor) => vendor.length > 0),
    ),
  ).sort((left, right) => left.localeCompare(right));
}

export function createPolicyKey(input: {
  api_type: string;
  model_match_type?: RoutingPolicyModelMatchType | null;
  model_match_value?: string | null;
}): string {
  return [
    input.api_type.trim(),
    input.model_match_type ?? EMPTY_MODEL_MATCH,
    input.model_match_value?.trim() ?? EMPTY_MODEL_MATCH,
  ].join("|");
}

export function getTargetMode(policy: RoutingPolicy): RoutingPolicyTargetMode {
  return policy.target_provider_id
    ? TARGET_MODE_EXACT_PROVIDER
    : TARGET_MODE_FILTERS;
}

export function toDraft(policy?: RoutingPolicy | null): RoutingPolicyDraft {
  if (!policy) {
    return createEmptyDraft();
  }

  const targetMode = getTargetMode(policy);

  return {
    api_type: policy.api_type,
    enabled: policy.enabled,
    model_match_type: policy.model_match_type ?? EMPTY_MODEL_MATCH,
    model_match_value: policy.model_match_value ?? "",
    target_mode: targetMode,
    target_provider_id: policy.target_provider_id ?? EMPTY_PROVIDER,
    allowed_group_ids:
      targetMode === TARGET_MODE_FILTERS ? policy.allowed_group_ids : [],
    allowed_vendors:
      targetMode === TARGET_MODE_FILTERS
        ? normalizeStringList(policy.allowed_vendors)
        : [],
  };
}

export function buildPayload(draft: RoutingPolicyDraft): RoutingPolicyInput {
  const normalizedMatchType =
    draft.model_match_type === EMPTY_MODEL_MATCH
      ? null
      : draft.model_match_type;
  const normalizedMatchValue = normalizedMatchType
    ? draft.model_match_value.trim()
    : null;
  const normalizedTargetProviderID =
    draft.target_mode === TARGET_MODE_EXACT_PROVIDER
      ? draft.target_provider_id || null
      : null;

  return {
    api_type: draft.api_type.trim(),
    enabled: draft.enabled,
    model_match_type: normalizedMatchType,
    model_match_value: normalizedMatchValue,
    target_provider_id: normalizedTargetProviderID,
    allowed_group_ids:
      normalizedTargetProviderID === null
        ? normalizeStringList(draft.allowed_group_ids)
        : [],
    allowed_vendors:
      normalizedTargetProviderID === null
        ? normalizeStringList(draft.allowed_vendors)
        : [],
  };
}

export function areStringSetsEqual(left: string[], right: string[]): boolean {
  const normalizedLeft = normalizeStringList(left)
    .slice()
    .sort((a, b) => a.localeCompare(b));
  const normalizedRight = normalizeStringList(right)
    .slice()
    .sort((a, b) => a.localeCompare(b));

  return normalizedLeft.join("|") === normalizedRight.join("|");
}

export function formatModelMatch(policy: RoutingPolicy): string {
  if (!policy.model_match_type || !policy.model_match_value) {
    return "api_type only";
  }

  return `${policy.model_match_type}: ${policy.model_match_value}`;
}

export function formatPolicyCount(policies: RoutingPolicy[]): string {
  const totalPolicies = policies.length;
  const enabledPolicies = policies.filter((policy) => policy.enabled).length;

  if (totalPolicies === 0) {
    return "No routing policies stored yet.";
  }

  return `${totalPolicies} policy${totalPolicies === 1 ? "" : "ies"} stored, ${enabledPolicies} enabled.`;
}

export function formatTargetMode(policy: RoutingPolicy): string {
  return getTargetMode(policy) === TARGET_MODE_EXACT_PROVIDER
    ? "Exact provider"
    : "Group/vendor filters";
}

export function toggleStringSelection(
  currentValues: string[],
  value: string,
  checked: boolean,
): string[] {
  return checked
    ? normalizeStringList([...currentValues, value])
    : currentValues.filter((currentValue) => currentValue !== value);
}

export function buildDraftWithAPIType(
  draft: RoutingPolicyDraft,
  providers: Provider[],
  nextAPIType: string,
): RoutingPolicyDraft {
  const nextCompatibleProviderIDs = new Set(
    getCompatibleProviders(providers, nextAPIType).map(
      (provider) => provider.id,
    ),
  );
  const shouldClearExactProvider =
    draft.target_mode === TARGET_MODE_EXACT_PROVIDER &&
    draft.target_provider_id !== EMPTY_PROVIDER &&
    !nextCompatibleProviderIDs.has(draft.target_provider_id);

  return {
    ...draft,
    api_type: nextAPIType,
    target_provider_id: shouldClearExactProvider
      ? EMPTY_PROVIDER
      : draft.target_provider_id,
  };
}

export function buildDraftWithTargetMode(
  draft: RoutingPolicyDraft,
  targetMode: RoutingPolicyTargetMode,
): RoutingPolicyDraft {
  if (targetMode === TARGET_MODE_EXACT_PROVIDER) {
    return {
      ...draft,
      target_mode: targetMode,
      allowed_group_ids: [],
      allowed_vendors: [],
    };
  }

  return {
    ...draft,
    target_mode: targetMode,
    target_provider_id: EMPTY_PROVIDER,
  };
}

export function getExactProviderPlaceholder(
  apiType: string,
  providersLoading: boolean,
): string {
  if (providersLoading) {
    return "Loading providers...";
  }
  if (apiType.trim()) {
    return "Select a provider";
  }
  return "Choose an API type first";
}

export function getVendorSelectionMessage(
  apiType: string,
  providersLoading: boolean,
  visibleVendors: string[],
): string | null {
  if (!apiType.trim()) {
    return "Choose an API type to derive vendor options.";
  }
  if (providersLoading && visibleVendors.length === 0) {
    return "Loading provider catalog...";
  }
  if (visibleVendors.length === 0) {
    return "No provider vendors available for this API type.";
  }
  return null;
}

export function getDeleteRoutingPolicyMessage(
  policy: RoutingPolicy | null,
): string {
  const apiType = policy?.api_type ?? "";
  return `Delete routing policy "${apiType}"? After deletion, matching requests may be handled by another active rule, fall back to normal provider selection, or fail closed based on the remaining policy set.`;
}

export function validateRoutingPolicyDraft(args: {
  draft: RoutingPolicyDraft;
  providers: Provider[];
  policies: RoutingPolicy[];
  editingPolicy: RoutingPolicy | null;
}): string | null {
  const { draft, providers, policies, editingPolicy } = args;
  const payload = buildPayload(draft);
  const compatibleProviders = getCompatibleProviders(
    providers,
    payload.api_type,
  );
  const compatibleProviderIDs = new Set(
    compatibleProviders.map((provider) => provider.id),
  );
  const availableVendorSet = new Set(
    getProviderDerivedVendors(providers, payload.api_type),
  );

  if (!payload.api_type) {
    return "API type is required.";
  }
  if (payload.model_match_type && !payload.model_match_value) {
    return "Model match value is required when a model match type is selected.";
  }

  if (payload.target_provider_id) {
    if (!compatibleProviderIDs.has(payload.target_provider_id)) {
      return "Select a provider that currently supports this API type.";
    }
  } else {
    if (
      payload.allowed_group_ids.length === 0 &&
      payload.allowed_vendors.length === 0
    ) {
      return "Select at least one allowed group or vendor.";
    }

    const selectedStaleVendors = payload.allowed_vendors.filter(
      (vendor) => !availableVendorSet.has(vendor),
    );
    const storedVendorSetCanSurvive =
      editingPolicy !== null &&
      editingPolicy.api_type === payload.api_type &&
      areStringSetsEqual(
        payload.allowed_vendors,
        editingPolicy.allowed_vendors,
      );

    if (selectedStaleVendors.length > 0 && !storedVendorSetCanSurvive) {
      return STALE_VENDOR_VALIDATION_MESSAGE;
    }
  }

  const duplicate = policies.find(
    (policy) =>
      policy.id !== editingPolicy?.id &&
      createPolicyKey(policy) === createPolicyKey(payload),
  );
  if (duplicate) {
    return "A rule with the same api_type and model match already exists.";
  }

  return null;
}
