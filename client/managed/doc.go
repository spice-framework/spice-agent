// Package managed coordinates attach-or-start behavior without owning endpoint
// discovery, process creation, or a transport implementation.
//
// A Connector starts a daemon only when Discovery returns ErrEndpointNotFound.
// Existing endpoints, including incompatible or unauthenticated endpoints, are
// never killed, replaced, or silently bypassed. Startup is serialized by a
// caller-supplied current-user lock and bounded by both the operation context
// and the configured timeout.
package managed
