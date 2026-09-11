package requestcapture

import (
	"fmt"
	"net/http"
	"testing"
)

// Message history lives for the lifetime of a WebSocket, so lookup cost must
// remain independent of transcript size while the shared session lock is held.
func BenchmarkWebSocketMessageLookup(b *testing.B) {
	for _, count := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("messages_%d", count), func(b *testing.B) {
			const quota = 256 << 20
			manager, err := NewManager(Config{ProcessCeilingBytes: quota, DefaultSessionQuotaBytes: quota})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = manager.Close() })
			if _, err := manager.Start(StartRequest{
				Providers:                 []ProviderIdentity{{ID: "selected"}},
				AcknowledgeRawPayloadRisk: true,
			}); err != nil {
				b.Fatal(err)
			}
			gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "lookup-benchmark"})
			recorder := gateway.BeginWebSocket(RawWebSocketStart{
				TargetURL: "wss://example.test/socket",
				Attempt:   AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
				Request:   RawRequest{Method: http.MethodGet},
			})
			recorder.ObserveWebSocketHandshake(WebSocketHandshake{StatusCode: http.StatusSwitchingProtocols})
			var last MessageRef
			for range count {
				last = recorder.MessageRead(MessageRead{
					Direction: MessageDirectionUpstreamToClient,
					Type:      MessageTypeText,
				})
				if !last.issued() {
					b.Fatal("message admission failed")
				}
			}
			access := recorder.acquire()
			record := access.record
			access.release()
			if record == nil || len(record.messages) != count {
				b.Fatal("transcript was not retained")
			}
			b.Run("lineage", func(b *testing.B) {
				for b.Loop() {
					if !record.hasMessageLineageLocked(last.lineage) {
						b.Fatal("lineage lookup failed")
					}
				}
			})
			b.Run("result", func(b *testing.B) {
				message := record.messages[len(record.messages)-1]
				result := MessageResult{Disposition: MessageDispositionSuppressed}
				for b.Loop() {
					message.resultSet = false
					recorder.MessageResult(last, result)
				}
				if !message.resultSet {
					b.Fatal("result lookup failed")
				}
			})
		})
	}
}
