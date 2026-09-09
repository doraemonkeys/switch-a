import { Link } from "react-router";
import { ArrowUpRight, ChevronDown, Fingerprint } from "lucide-react";
import type { LoginView } from "@/api/client-disguise/types";

export function LoginIdentity({ login }: { login: LoginView }) {
  return (
    <section className="cd-identity-section">
      <details className="cd-disclosure">
        <summary>
          <span>
            <Fingerprint size={17} aria-hidden="true" />
            <strong>Device identity</strong>
            <small>
              {login.identity
                ? "Persistent identity assigned"
                : "Created on first eligible request"}
            </small>
          </span>
          <ChevronDown size={16} aria-hidden="true" />
        </summary>
        <dl className="cd-detail-grid">
          <div>
            <dt>Device ID</dt>
            <dd>
              {login.identity?.device_id ??
                "Unbound — created atomically before the first eligible send"}
            </dd>
          </div>
          <div>
            <dt>Identity generation</dt>
            <dd>{login.identity?.generation_id ?? "Not yet created"}</dd>
          </div>
          <div>
            <dt>Credential session</dt>
            <dd>{login.credential_session_id}</dd>
          </div>
        </dl>
      </details>
      <div className="cd-providers">
        <span className="cd-kicker">LINKED PROVIDERS</span>
        <div className="cd-provider-list">
          {login.providers.map((provider) => (
            <Link
              key={provider.provider_id}
              to="/providers"
              className="cd-provider"
            >
              <i
                className={
                  provider.client_disguise.enabled ? "cd-provider-on" : ""
                }
                aria-hidden="true"
              />
              {provider.provider_name}
              <span>
                {provider.client_disguise.enabled ? "Enabled" : "Disabled"}
              </span>
              <ArrowUpRight size={12} aria-hidden="true" />
            </Link>
          ))}
          {login.providers.length === 0 && (
            <p className="cd-description">
              No providers linked.{" "}
              <Link to="/providers">
                Manage providers <ArrowUpRight size={12} aria-hidden="true" />
              </Link>
            </p>
          )}
        </div>
      </div>
    </section>
  );
}
