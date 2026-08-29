export interface CredentialRouteReference {
  provider_id: string;
  provider_name: string;
  api_type: string;
}

export interface RenameCredentialSessionInput {
  expected_version: number;
  name: string;
}

export interface NewProviderCredentialSessionInput {
  id: string;
  name: string;
  kind: "api_key";
  secret_data: string;
}
