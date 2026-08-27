package admin

import "encoding/json"

func fullConfigImportScope() *ConfigImportScope {
	return &ConfigImportScope{Mode: ConfigImportModeFull}
}

func selectionConfigImportScope(groupIDs, providerIDs []string) *ConfigImportScope {
	return &ConfigImportScope{
		Mode: ConfigImportModeSelection,
		Selection: &ConfigImportSelection{
			GroupIDs:    groupIDs,
			ProviderIDs: providerIDs,
		},
	}
}

func importRequestFromExport(exported ExportedConfig) ImportConfigRequest {
	return ImportConfigRequest{
		Version:            exported.Version,
		ImportScope:        fullConfigImportScope(),
		Providers:          exported.Providers,
		CredentialSessions: exported.CredentialSessions,
		Groups:             exported.Groups,
		RoutingPolicies:    exported.RoutingPolicies,
		Settings:           exported.Settings,
		InternalErrorRules: exported.InternalErrorRules,
	}
}

func (req ImportConfigRequest) MarshalJSON() ([]byte, error) {
	type importConfigRequest ImportConfigRequest

	payload := importConfigRequest(req)
	if payload.ImportScope == nil {
		payload.ImportScope = fullConfigImportScope()
	}
	return json.Marshal(payload)
}
