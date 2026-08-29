import type { CredentialReauthenticationRequirement } from "../../api/types";

export function ReauthenticationNotice({
  requirements,
  imported = false,
}: {
  requirements: CredentialReauthenticationRequirement[];
  imported?: boolean;
}) {
  if (requirements.length === 0) {
    return null;
  }

  return (
    <div className="bg-warning/10 border border-warning/20 rounded-lg p-4 space-y-2">
      <h3 className="text-sm font-medium text-warning">
        {requirements.length} 个 ChatGPT 登录需要重新认证
      </h3>
      <p className="text-sm text-text-secondary">
        {imported
          ? "Provider 配置已经恢复，但重新认证前不会参与请求路由。请到 Providers 编辑对应 Provider，并点击 Reconnect GPT。"
          : "配置可以正常导入；出于凭据安全考虑，导出文件不包含 ChatGPT 登录信息。导入后请到 Providers 编辑对应 Provider，并点击 Reconnect GPT。"}
      </p>
      <ul className="space-y-1 text-sm text-text-secondary">
        {requirements.map((requirement) => (
          <li key={requirement.credential_session_id}>
            <span className="text-text-primary">{requirement.name}</span>
            <span className="text-text-muted">
              {" "}
              ({requirement.credential_session_id})
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
