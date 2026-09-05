import type { ProfileRevision } from "@/api/client-disguise/types";
export function ProfileSummary({ profile }: { profile: ProfileRevision }) {
  return (
    <div className="rounded-lg bg-bg-secondary p-3 text-sm">
      <p>
        {profile.evidence_kind === "source" ? (
          "Source-backed profile; unspecified environment features preserve the incoming client values."
        ) : (
          <>
            Captured: {profile.captured_at} · Source: {profile.source_id}
          </>
        )}
      </p>
      {profile.source_url && (
        <a
          className="text-primary underline"
          href={profile.source_url}
          target="_blank"
          rel="noreferrer"
        >
          Profile evidence source
        </a>
      )}
      <p>
        Feature scope:{" "}
        {Object.entries(profile.features)
          .filter(([, value]) =>
            typeof value === "string"
              ? value.length > 0
              : value != null && Object.keys(value).length > 0,
          )
          .map(([key]) => key.replaceAll("_", " "))
          .join(", ") || "identity mappings only"}
      </p>
      <p className="break-all">
        User-Agent: {profile.features.user_agent || "Unchanged"}
      </p>
      <p>Originator: {profile.features.originator || "Unchanged"}</p>
      <p>
        Application identity and observed profile fields apply. Transport
        characteristics require an independently selected sample.
      </p>
    </div>
  );
}
