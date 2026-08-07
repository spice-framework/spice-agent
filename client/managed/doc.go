// Package managed coordinates attach-or-start behavior while retaining exact
// ownership of only the daemon candidate it launches. Process creation,
// endpoint discovery, and transport implementations remain injected.
//
// A Connector starts a daemon only when Discovery returns ErrEndpointNotFound.
// Existing endpoints, including incompatible or unauthenticated endpoints, are
// never killed, replaced, or silently bypassed. Startup is serialized by a
// caller-supplied current-user lock and bounded by both the operation context
// and the configured timeout. Failed or canceled launches are shut down and
// joined; Shutdown never targets a daemon found through Discovery.
package managed
