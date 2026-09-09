# WebSocket transport dependency

Source: `github.com/coder/websocket v1.8.15`. The original MIT license is in
[LICENSE.txt](LICENSE.txt). Source and tests are retained locally so every release
uses the same transport behavior; do not edit the Go module cache.

Switch-A changes:

- Prioritize queued Ping/Pong/Close frames at the next complete frame boundary.
- Keep control payload reading, frame-boundary waiting and physical writing as
  separate operations. The five-second control I/O limit starts when that I/O can
  begin; the caller's deadline still bounds the whole operation.
- Retain cancellation and connection-close wakeups while waiting for a frame.
- Colocate the unchanged HTTP hijacker helper in `accept.go`, keeping this folder
  within the repository's 20 source-file limit (including assembly files).

A frame already being transmitted cannot be interrupted. These changes do not
guarantee heartbeats during an unwritable TCP connection or change message data,
masking, fragmentation, handshake headers or compression negotiation.

Validation from the repository root:
`go test -race github.com/coder/websocket/...`

When updating upstream, retain the local control scheduling regression tests and
review `accept.go` (includes upstream `hijack.go`), `conn.go`, `read.go`,
`write.go` and `internal/framegate` against the new
version. Unmodified upstream files should stay synchronized with that version.
