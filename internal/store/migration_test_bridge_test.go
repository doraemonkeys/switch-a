package store

const (
	requestLogCreatedAtUnixNanoColumn        = "created_at_unix_nano"
	requestLogCreatedAtUnixNanoIndex         = "idx_request_logs_created_at_unix_nano"
	requestLogProviderCreatedAtUnixNanoIndex = "idx_request_logs_provider_created_at_unix_nano"
	requestLogModelCreatedAtUnixNanoIndex    = "idx_request_logs_model_created_at_unix_nano"
	requestLogAPITypeCreatedAtUnixNanoIndex  = "idx_request_logs_api_type_created_at_unix_nano"
	legacyRequestLogProviderCreatedAtIndex   = "idx_request_logs_provider_created_at"
	legacyRequestLogModelCreatedAtIndex      = "idx_request_logs_model_created_at"
	legacyRequestLogAPITypeCreatedAtIndex    = "idx_request_logs_api_type_created_at"
)

var legacyRequestLogAnalyticsIndexes = []string{
	legacyRequestLogProviderCreatedAtIndex,
	legacyRequestLogModelCreatedAtIndex,
	legacyRequestLogAPITypeCreatedAtIndex,
}
