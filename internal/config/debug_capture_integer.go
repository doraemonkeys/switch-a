package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"
)

type debugCaptureIntegerField struct {
	key         string
	environment string
	assign      func(*serializedConfig, int64)
}

var debugCaptureIntegerFields = [...]debugCaptureIntegerField{
	{KeyDebugCaptureMemoryCeilingMiB, EnvDebugCaptureMemoryCeilingMiB, func(config *serializedConfig, value int64) { config.DebugCaptureMemoryCeilingMiB = value }},
	{KeyDebugCaptureMaxActiveRecords, EnvDebugCaptureMaxActiveRecords, func(config *serializedConfig, value int64) { config.DebugCaptureMaxActiveRecords = value }},
	{KeyDebugCaptureMaxActiveTraces, EnvDebugCaptureMaxActiveTraces, func(config *serializedConfig, value int64) { config.DebugCaptureMaxActiveTraces = value }},
	{KeyDebugCaptureMaxTransitionsPerTrace, EnvDebugCaptureMaxTransitionsPerTrace, func(config *serializedConfig, value int64) { config.DebugCaptureMaxTransitionsPerTrace = value }},
	{KeyDebugCaptureMaxPendingExports, EnvDebugCaptureMaxPendingExports, func(config *serializedConfig, value int64) { config.DebugCaptureMaxPendingExports = value }},
	{KeyDebugCaptureMaxConcurrentDownloads, EnvDebugCaptureMaxConcurrentDownloads, func(config *serializedConfig, value int64) { config.DebugCaptureMaxConcurrentDownloads = value }},
	{KeyDebugCaptureDetailPreviewBytes, EnvDebugCaptureDetailPreviewBytes, func(config *serializedConfig, value int64) { config.DebugCaptureDetailPreviewBytes = value }},
	{KeyDebugCaptureDetailEventLimit, EnvDebugCaptureDetailEventLimit, func(config *serializedConfig, value int64) { config.DebugCaptureDetailEventLimit = value }},
	{KeyDebugCaptureDownloadTokenTTLSeconds, EnvDebugCaptureDownloadTokenTTLSeconds, func(config *serializedConfig, value int64) { config.DebugCaptureDownloadTokenTTLSeconds = value }},
	{KeyDebugCaptureMaxRecordsPerProvider, EnvDebugCaptureMaxRecordsPerProvider, func(config *serializedConfig, value int64) { config.DebugCaptureMaxRecordsPerProvider = value }},
	{KeyDebugCaptureChunkBytes, EnvDebugCaptureChunkBytes, func(config *serializedConfig, value int64) { config.DebugCaptureChunkBytes = value }},
	{KeyDebugCaptureExportLineBytes, EnvDebugCaptureExportLineBytes, func(config *serializedConfig, value int64) { config.DebugCaptureExportLineBytes = value }},
}

// decodeDebugCaptureIntegers bypasses Viper's weak integer conversion for
// safety limits. Preserving the effective source value prevents base-0 parsing,
// float truncation, and precision loss before runtime validation sees it.
func decodeDebugCaptureIntegers(
	v *viper.Viper,
	destination *serializedConfig,
	fileValues map[string]int64,
) error {
	for _, field := range debugCaptureIntegerFields {
		var raw any
		if environmentValue, exists := os.LookupEnv(field.environment); exists {
			raw = environmentValue
		} else if fileValue, exists := fileValues[field.key]; exists {
			raw = fileValue
		} else {
			raw = v.Get(field.key)
		}

		value, err := exactConfigInteger(field.key, raw)
		if err != nil {
			return err
		}
		field.assign(destination, value)
	}
	return nil
}

func exactConfigInteger(key string, raw any) (int64, error) {
	if raw == nil {
		return 0, fmt.Errorf("%s must be a canonical base-10 integer", key)
	}

	value := reflect.ValueOf(raw)
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		unsigned := value.Uint()
		maxInt64 := ^uint64(0) >> 1
		if unsigned > maxInt64 {
			return 0, fmt.Errorf("%s is too large to represent as an integer", key)
		}
		return int64(unsigned), nil
	case reflect.String:
		parsed, err := parseCanonicalDecimal(value.String())
		if err != nil {
			return 0, fmt.Errorf("%s must be a canonical base-10 integer: %w", key, err)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("%s must be a canonical base-10 integer", key)
	}
}

func parseCanonicalDecimal(raw string) (int64, error) {
	if raw == "" {
		return 0, fmt.Errorf("value is empty")
	}

	digitStart := 0
	if raw[0] == '-' {
		digitStart = 1
		if len(raw) == 1 {
			return 0, fmt.Errorf("value has no digits")
		}
	}
	if len(raw)-digitStart > 1 && raw[digitStart] == '0' {
		return 0, fmt.Errorf("leading zeroes are not allowed")
	}
	for index := digitStart; index < len(raw); index++ {
		if raw[index] < '0' || raw[index] > '9' {
			return 0, fmt.Errorf("value contains a non-decimal digit")
		}
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("value is outside the signed 64-bit range")
	}
	return value, nil
}

func decodeDebugCaptureFileIntegers(configFile string) (map[string]int64, error) {
	extension := strings.ToLower(filepath.Ext(configFile))
	if extension != ".yaml" && extension != ".yml" && extension != ".json" {
		return nil, nil
	}

	content, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("read config for integer validation: %w", err)
	}
	if extension == ".json" {
		return decodeJSONDebugCaptureIntegers(content)
	}
	return decodeYAMLDebugCaptureIntegers(content)
}

func decodeYAMLDebugCaptureIntegers(content []byte) (map[string]int64, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("parse config for integer validation: %w", err)
	}
	values := make(map[string]int64)
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return values, nil
	}

	root := document.Content[0]
	for index := 0; index+1 < len(root.Content); index += 2 {
		keyNode := root.Content[index]
		valueNode := root.Content[index+1]
		field, exists := debugCaptureIntegerFieldForKey(keyNode.Value)
		if !exists {
			continue
		}
		if _, overridden := os.LookupEnv(field.environment); overridden {
			continue
		}

		value, err := exactYAMLInteger(field.key, valueNode)
		if err != nil {
			return nil, err
		}
		values[field.key] = value
	}
	return values, nil
}

func exactYAMLInteger(key string, node *yaml.Node) (int64, error) {
	visited := make(map[*yaml.Node]struct{})
	for node != nil && node.Kind == yaml.AliasNode {
		if _, exists := visited[node]; exists {
			return 0, fmt.Errorf("%s contains a cyclic YAML alias", key)
		}
		visited[node] = struct{}{}
		node = node.Alias
	}
	if node == nil || node.Kind != yaml.ScalarNode || (node.Tag != "!!int" && node.Tag != "!!str") {
		return 0, fmt.Errorf("%s must be a canonical base-10 integer", key)
	}
	value, err := parseCanonicalDecimal(node.Value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a canonical base-10 integer: %w", key, err)
	}
	return value, nil
}

func decodeJSONDebugCaptureIntegers(content []byte) (map[string]int64, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("parse config for integer validation: %w", err)
	}
	if err := ensureJSONDocumentEnded(decoder); err != nil {
		return nil, err
	}

	values := make(map[string]int64)
	for key, raw := range document {
		field, exists := debugCaptureIntegerFieldForKey(key)
		if !exists {
			continue
		}
		if _, overridden := os.LookupEnv(field.environment); overridden {
			continue
		}

		var literal string
		switch value := raw.(type) {
		case json.Number:
			literal = value.String()
		case string:
			literal = value
		default:
			return nil, fmt.Errorf("%s must be a canonical base-10 integer", field.key)
		}
		value, err := parseCanonicalDecimal(literal)
		if err != nil {
			return nil, fmt.Errorf("%s must be a canonical base-10 integer: %w", field.key, err)
		}
		values[field.key] = value
	}
	return values, nil
}

func ensureJSONDocumentEnded(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("parse config for integer validation: multiple JSON values")
		}
		return fmt.Errorf("parse config for integer validation: %w", err)
	}
	return nil
}

func debugCaptureIntegerFieldForKey(key string) (debugCaptureIntegerField, bool) {
	for _, field := range debugCaptureIntegerFields {
		if strings.EqualFold(field.key, key) {
			return field, true
		}
	}
	return debugCaptureIntegerField{}, false
}
