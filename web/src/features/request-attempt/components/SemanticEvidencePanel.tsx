import type {
  SemanticErrorEvidence,
  SemanticRuleSnapshot,
} from "@/api/evidence-types";
import {
  EvidenceCode,
  EvidenceField,
  EvidenceGrid,
  EvidenceSection,
} from "./EvidencePrimitives";
import { formatEvidenceToken } from "../evidence/view-model";

function TokenValue({ value }: { value: string }) {
  return (
    <span>
      {formatEvidenceToken(value)} <EvidenceCode>({value})</EvidenceCode>
    </span>
  );
}

function BooleanValue({ value }: { value: boolean }) {
  return value ? "Yes" : "No";
}

function RuleAction({ snapshot }: { snapshot: SemanticRuleSnapshot }) {
  const action = snapshot.action;
  if (action.type === "passthrough") {
    return <TokenValue value={action.type} />;
  }
  return (
    <span>
      <TokenValue value={action.type} /> · {action.max_retries} retries ·
      backoff {action.backoff.initial_delay}–{action.backoff.max_delay} ×{" "}
      {action.backoff.multiplier}
      {action.backoff.jitter ? " with jitter" : " without jitter"}
    </span>
  );
}

function DecisionSection({ evidence }: { evidence: SemanticErrorEvidence }) {
  return (
    <EvidenceSection title="Semantic decision">
      <EvidenceGrid>
        <EvidenceField
          label="Decision"
          value={<TokenValue value={evidence.decision.value} />}
        />
        <EvidenceField
          label="Decision reason"
          value={<TokenValue value={evidence.decision.reason} />}
        />
      </EvidenceGrid>
    </EvidenceSection>
  );
}

function RuleSection({ evidence }: { evidence: SemanticErrorEvidence }) {
  const { rule } = evidence;
  const snapshot = rule.normalized_snapshot;
  const scope =
    snapshot.target.kind === "global"
      ? "Global"
      : `Provider ${snapshot.target.provider_id}`;
  return (
    <EvidenceSection title="Winning rule and matches">
      <EvidenceGrid>
        <EvidenceField label="Rule" value={snapshot.name} />
        <EvidenceField
          label="Winner ID"
          value={<EvidenceCode>{rule.winner_id}</EvidenceCode>}
        />
        <EvidenceField label="Scope" value={scope} />
        <EvidenceField
          label="Action"
          value={<RuleAction snapshot={snapshot} />}
        />
        <EvidenceField label="Revision" value={rule.revision} />
        <EvidenceField label="Position" value={snapshot.position} />
        <EvidenceField
          label="Enabled in snapshot"
          value={<BooleanValue value={snapshot.enabled} />}
        />
        <EvidenceField
          label="API type"
          value={snapshot.api_type ?? "All APIs"}
        />
        <EvidenceField
          label="Match mode"
          value={<TokenValue value={snapshot.match_mode} />}
        />
        <EvidenceField
          label="Matched fields"
          value={rule.matched_fields.join(", ")}
        />
      </EvidenceGrid>

      <div className="mt-3">
        <p className="text-[11px] uppercase tracking-wide text-text-muted">
          Matched keywords
        </p>
        <ol className="mt-1 space-y-1 text-sm text-text-primary">
          {rule.matched_keywords.map((keyword, index) => (
            <li key={`${rule.matched_keyword_indexes[index]}:${keyword}`}>
              <EvidenceCode>
                [{rule.matched_keyword_indexes[index]}] {keyword}
              </EvidenceCode>
            </li>
          ))}
        </ol>
      </div>

      <details className="mt-3 text-sm text-text-secondary">
        <summary className="cursor-pointer">
          {rule.matching_rule_ids.length} matching rule
          {rule.matching_rule_ids.length === 1 ? "" : "s"}
        </summary>
        <ol className="mt-2 max-h-40 space-y-1 overflow-y-auto pl-5 list-decimal">
          {rule.matching_rule_ids.map((id) => (
            <li key={id}>
              <EvidenceCode>{id}</EvidenceCode>
            </li>
          ))}
        </ol>
      </details>

      <details className="mt-2 text-sm text-text-secondary">
        <summary className="cursor-pointer">
          {snapshot.keywords.length} normalized rule keyword
          {snapshot.keywords.length === 1 ? "" : "s"}
        </summary>
        <ol className="mt-2 max-h-40 space-y-1 overflow-y-auto pl-5 list-decimal">
          {snapshot.keywords.map((keyword) => (
            <li key={keyword}>
              <EvidenceCode>{keyword}</EvidenceCode>
            </li>
          ))}
        </ol>
      </details>
    </EvidenceSection>
  );
}

function ResponseSection({ evidence }: { evidence: SemanticErrorEvidence }) {
  const { response } = evidence;
  return (
    <EvidenceSection title="Response boundary">
      <EvidenceGrid>
        <EvidenceField
          label="Protocol"
          value={<EvidenceCode>{response.protocol_id}</EvidenceCode>}
        />
        <EvidenceField
          label="State"
          value={<TokenValue value={response.state} />}
        />
        <EvidenceField
          label="Match timing"
          value={<TokenValue value={response.match_timing} />}
        />
        <EvidenceField
          label="Boundary reason"
          value={<TokenValue value={response.boundary_reason} />}
        />
        <EvidenceField label="Elapsed" value={`${response.elapsed_ms} ms`} />
        <EvidenceField
          label="Peak probe bytes"
          value={response.peak_probe_bytes}
        />
        <EvidenceField
          label="Raw probe bytes"
          value={response.raw_probe_bytes}
        />
        <EvidenceField
          label="Decoded probe bytes"
          value={response.decoded_probe_bytes}
        />
        <EvidenceField
          label="Upstream bytes read"
          value={response.upstream_bytes_read}
        />
        <EvidenceField
          label="Client body bytes written"
          value={response.client_body_bytes_written}
        />
        <EvidenceField
          label="Headers committed"
          value={<BooleanValue value={response.headers_committed} />}
        />
        <EvidenceField
          label="Visible to client"
          value={<BooleanValue value={response.visible_to_client} />}
        />
      </EvidenceGrid>
    </EvidenceSection>
  );
}

function RetrySection({ evidence }: { evidence: SemanticErrorEvidence }) {
  const { retry } = evidence;
  return (
    <EvidenceSection title="Retry budget">
      <EvidenceGrid>
        <EvidenceField
          label="Rule action"
          value={<TokenValue value={retry.action} />}
        />
        <EvidenceField
          label="Rule retries scheduled"
          value={retry.rule_retries_scheduled}
        />
        <EvidenceField
          label="Rule retry limit"
          value={retry.rule_retry_limit}
        />
        <EvidenceField
          label="Global attempts started"
          value={retry.global_attempts_started}
        />
        <EvidenceField
          label="Global attempts remaining"
          value={retry.global_attempts_remaining ?? "Not applicable"}
        />
        <EvidenceField
          label="Global attempts unlimited"
          value={<BooleanValue value={retry.global_attempts_unlimited} />}
        />
      </EvidenceGrid>
    </EvidenceSection>
  );
}

function AlternateSection({ evidence }: { evidence: SemanticErrorEvidence }) {
  const { alternate } = evidence;
  return (
    <EvidenceSection title="Alternate provider">
      <EvidenceGrid>
        <EvidenceField
          label="Reservation outcome"
          value={<TokenValue value={alternate.outcome} />}
        />
        <EvidenceField
          label="Provider"
          value={alternate.provider_id ?? "Not selected"}
        />
        <EvidenceField
          label="Switch mode"
          value={
            alternate.switch_mode ? (
              <TokenValue value={alternate.switch_mode} />
            ) : (
              "Not set"
            )
          }
        />
        <EvidenceField
          label="Switch reason"
          value={
            alternate.switch_reason ? (
              <TokenValue value={alternate.switch_reason} />
            ) : (
              "Not set"
            )
          }
        />
      </EvidenceGrid>
    </EvidenceSection>
  );
}

function HealthSection({ evidence }: { evidence: SemanticErrorEvidence }) {
  const { health } = evidence;
  return (
    <EvidenceSection title="Health attribution">
      <EvidenceGrid>
        <EvidenceField
          label="Verdict"
          value={<TokenValue value={health.verdict} />}
        />
        <EvidenceField
          label="Cause"
          value={<TokenValue value={health.cause} />}
        />
        <EvidenceField
          label="Circuit opened"
          value={<BooleanValue value={health.circuit_opened} />}
        />
      </EvidenceGrid>
    </EvidenceSection>
  );
}

function IdentitySection({ evidence }: { evidence: SemanticErrorEvidence }) {
  const { identity } = evidence;
  return (
    <EvidenceSection title="Attempt identity">
      <EvidenceGrid>
        <EvidenceField label="Request ID" value={identity.request_id} />
        <EvidenceField label="Operation ID" value={identity.operation_id} />
        <EvidenceField label="Provider ID" value={identity.provider_id} />
        <EvidenceField
          label="Logical attempt"
          value={identity.logical_attempt}
        />
        <EvidenceField
          label="Provider attempt"
          value={identity.provider_attempt}
        />
        <EvidenceField
          label="Credential phase"
          value={identity.credential_phase}
        />
      </EvidenceGrid>
    </EvidenceSection>
  );
}

export function SemanticEvidencePanel({
  evidence,
}: {
  evidence: SemanticErrorEvidence;
}) {
  return (
    <div className="space-y-3" aria-label="Semantic error evidence">
      <DecisionSection evidence={evidence} />
      <RuleSection evidence={evidence} />
      <ResponseSection evidence={evidence} />
      <RetrySection evidence={evidence} />
      <AlternateSection evidence={evidence} />
      <HealthSection evidence={evidence} />
      <IdentitySection evidence={evidence} />
    </div>
  );
}
