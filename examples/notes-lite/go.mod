module example.com/notes-lite

go 1.27.4

require (
	connectrpc.com/connect v1.21.1
	github.com/frostyard/hearth v0.0.0-20261116071607-859e17e3e4c6
	github.com/modelcontextprotocol/go-sdk v1.8.0
	google.golang.org/protobuf v1.36.12
	modernc.org/sqlite v1.62.0
)

require (
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.20.4 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/nats-io/nats.go v1.56.0 // indirect
	github.com/nats-io/nkeys v0.4.17 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	modernc.org/libc v1.80.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/frostyard/hearth => ../..

tool (
	connectrpc.com/connect/cmd/protoc-gen-connect-go
	github.com/frostyard/hearth/cmd/hearth
	github.com/frostyard/hearth/cmd/protoc-gen-hearth-authz
	github.com/frostyard/hearth/cmd/protoc-gen-hearth-events
	github.com/frostyard/hearth/cmd/protoc-gen-hearth-manifest
	github.com/frostyard/hearth/cmd/protoc-gen-hearth-mcp
)
