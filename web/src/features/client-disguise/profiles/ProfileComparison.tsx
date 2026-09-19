import type { ProfileRevision } from "@/api/client-disguise/types";
import { captureLabel } from "./profileCatalog";
import { featureDifferences, UNOBSERVED } from "./profileFeatures";

export function ProfileComparison({
  profile,
  baseline,
}: {
  profile: ProfileRevision;
  baseline: ProfileRevision;
}) {
  const differences = featureDifferences(baseline, profile);
  return (
    <details className="cd-snapshot-comparison">
      <summary>
        对比变化{differences.length > 0 && `（${differences.length}）`}
      </summary>
      <p className="cd-field-help">
        基准：{captureLabel(baseline)} · 此快照：{captureLabel(profile)}
      </p>
      {differences.length ? (
        <table aria-label="快照字段差异">
          <thead>
            <tr>
              <th>字段</th>
              <th>基准快照</th>
              <th>此快照</th>
            </tr>
          </thead>
          <tbody>
            {differences.map((field) => (
              <tr key={field.key}>
                <th scope="row">{field.label}</th>
                <td>{field.from || UNOBSERVED}</td>
                <td>{field.to || UNOBSERVED}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p className="cd-field-help">环境特征相同，仅采集记录或来源不同。</p>
      )}
      <details className="cd-snapshot-identifiers">
        <summary>对比记录 ID</summary>
        <dl>
          <dt>基准快照</dt>
          <dd>{baseline.id}</dd>
          <dt>此快照</dt>
          <dd>{profile.id}</dd>
        </dl>
      </details>
    </details>
  );
}
