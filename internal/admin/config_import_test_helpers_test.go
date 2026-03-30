package admin

import "encoding/json"

func fullConfigImportScope() *ConfigImportScope {
	return &ConfigImportScope{Mode: ConfigImportModeFull}
}

func importRequestFromExport(exported ExportedConfig) ImportConfigRequest {
	return ImportConfigRequest{
		Version:         exported.Version,
		ImportScope:     fullConfigImportScope(),
		Providers:       exported.Providers,
		Groups:          exported.Groups,
		RoutingPolicies: exported.RoutingPolicies,
		Settings:        exported.Settings,
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
