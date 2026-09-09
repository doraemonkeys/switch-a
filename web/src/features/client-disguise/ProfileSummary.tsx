import { ArrowUpRight, ChevronDown, Monitor } from "lucide-react";
import type { ProfileRevision } from "@/api/client-disguise/types";

export function ProfileSummary({ profile }: { profile: ProfileRevision }) {
  const features = Object.entries(profile.features).filter(([, value]) =>
    typeof value === "string"
      ? value.length > 0
      : value != null && Object.keys(value).length > 0,
  );
  return (
    <div className="cd-profile-preview">
      <div className="cd-preview-heading">
        <span className="cd-preview-icon">
          <Monitor size={22} aria-hidden="true" />
        </span>
        <div>
          <p className="cd-kicker">SELECTED PROFILE</p>
          <strong>
            {profile.tuple.client_type} <span>·</span>{" "}
            {profile.client_version || "Unversioned"}
          </strong>
        </div>
        <span className="cd-platform">
          {profile.tuple.platform} / {profile.tuple.arch}
        </span>
      </div>
      <p className="cd-description">
        {profile.evidence_kind === "source"
          ? "Source-backed profile. Unspecified environment features keep the incoming client values."
          : `Captured ${profile.captured_at} · Source: ${profile.source_id}`}
      </p>
      <details className="cd-profile-details">
        <summary>
          Inspect profile fields <ChevronDown size={14} aria-hidden="true" />
        </summary>
        <dl className="cd-detail-grid">
          <div>
            <dt>Revision ID</dt>
            <dd>{profile.id}</dd>
          </div>
          <div>
            <dt>User-Agent</dt>
            <dd>{profile.features.user_agent || "Unchanged"}</dd>
          </div>
          <div>
            <dt>Originator</dt>
            <dd>{profile.features.originator || "Unchanged"}</dd>
          </div>
          <div>
            <dt>Feature scope</dt>
            <dd>
              {features.map(([key]) => key.replaceAll("_", " ")).join(", ") ||
                "Identity mappings only"}
            </dd>
          </div>
        </dl>
        {profile.source_url && (
          <a
            className="cd-text-link"
            href={profile.source_url}
            target="_blank"
            rel="noreferrer"
          >
            Profile evidence source{" "}
            <ArrowUpRight size={13} aria-hidden="true" />
          </a>
        )}
        <p className="cd-field-help">
          Application identity and observed fields apply. Transport
          characteristics require an independently selected sample.
        </p>
      </details>
    </div>
  );
}
