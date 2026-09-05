package main

import (
	"context"
	codexkeyring "github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

func loadApplicationPortableHMAC(ctx context.Context, persistence any) ([]codexkeyring.HMACMaterial, error) {
	if source, ok := persistence.(interface {
		LoadCodexHMAC(context.Context) ([]codexkeyring.HMACMaterial, error)
	}); ok {
		return source.LoadCodexHMAC(ctx)
	}
	return nil, nil
}
func fileRequiredHMAC(required []string, portable []codexkeyring.HMACMaterial) []string {
	restored := map[string]bool{}
	for _, key := range portable {
		restored[key.Version] = true
	}
	result := make([]string, 0, len(required))
	for _, version := range required {
		if !restored[version] {
			result = append(result, version)
		}
	}
	return result
}
