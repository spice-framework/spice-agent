// Package grpcclient adapts the engine/v1 gRPC protocol to the transport-neutral
// client contracts. It authenticates every RPC, validates every protocol
// response before exposing it, and never retries mutations automatically.
//
// A transport failure while initializing a fresh client or reconnecting is
// reported as non-retryable and uncertain, except for definitive
// authentication and invalid-request rejections. Protocol v1 cannot replay a
// result lost after the server commits client allocation or its ownership CAS,
// so callers must not retry automatically.
package grpcclient
