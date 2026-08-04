package apicontract

const APICatalogSchemaVersion = 1

// CatalogResponse is the strict public projection served by the admin API.
// Internal routing and error-family details deliberately stay server-side.
type CatalogResponse struct {
	SchemaVersion       int                    `json:"schema_version"`
	CustomAPITypePrefix string                 `json:"custom_api_type_prefix"`
	APITypes            []CatalogResponseEntry `json:"api_types"`
}

type CatalogResponseEntry struct {
	APIType                string               `json:"api_type"`
	Label                  string               `json:"label"`
	Description            string               `json:"description"`
	DisplayOrder           int                  `json:"display_order"`
	SemanticErrorSupported bool                 `json:"semantic_error_supported"`
	ResponseProtocolIDs    []ResponseProtocolID `json:"response_protocol_ids"`
}

// Projection constructs a fresh response so handlers may encode concurrently
// without exposing the process-wide catalog to mutation.
func Projection() CatalogResponse {
	response := CatalogResponse{
		SchemaVersion:       APICatalogSchemaVersion,
		CustomAPITypePrefix: CustomAPITypePrefix,
		APITypes:            make([]CatalogResponseEntry, 0, len(definitions)),
	}
	for _, definition := range definitions {
		response.APITypes = append(response.APITypes, CatalogResponseEntry{
			APIType:                string(definition.APIType),
			Label:                  definition.Label,
			Description:            definition.Description,
			DisplayOrder:           definition.DisplayOrder,
			SemanticErrorSupported: definition.SemanticErrorSupported,
			ResponseProtocolIDs:    append([]ResponseProtocolID(nil), definition.ResponseProtocolIDs...),
		})
	}
	return response
}
