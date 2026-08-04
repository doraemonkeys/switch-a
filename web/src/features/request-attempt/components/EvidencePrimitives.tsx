import type { ReactNode } from "react";
import {
  FIELD_LABEL_CLASS,
  SECTION_CARD_CLASS,
  SNIPPET_CLASS,
} from "../evidence/view-model";

export function EvidenceSection({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <section className={SECTION_CARD_CLASS} aria-label={title}>
      <h4 className="text-sm font-medium text-text-primary">{title}</h4>
      {children}
    </section>
  );
}

export function EvidenceField({
  label,
  value,
}: {
  label: string;
  value: ReactNode;
}) {
  return (
    <div>
      <dt className={FIELD_LABEL_CLASS}>{label}</dt>
      <dd className="mt-1 text-sm text-text-primary break-words">{value}</dd>
    </div>
  );
}

export function EvidenceGrid({ children }: { children: ReactNode }) {
  return <dl className="mt-3 grid gap-3 sm:grid-cols-2">{children}</dl>;
}

export function EvidenceSnippet({
  label,
  text,
}: {
  label: string;
  text: string | undefined;
}) {
  if (!text) {
    return null;
  }
  return (
    <div className="mt-3">
      <p className={FIELD_LABEL_CLASS}>{label}</p>
      <pre className={SNIPPET_CLASS}>{text}</pre>
    </div>
  );
}

export function EvidenceCode({ children }: { children: ReactNode }) {
  return <code className="font-mono text-xs">{children}</code>;
}
