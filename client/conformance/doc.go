// Package conformance provides a black-box contract suite for implementations
// of the transport-neutral client Connector and Session interfaces.
//
// The suite does not discover, authenticate, launch, or retain a daemon. A
// caller supplies one already-configured Connector for a disposable canonical
// fixture. The fixture must advertise the Waiting definition, complete one
// ordinary text run, and keep Waiting active until cancellation. The initial
// profile validates interaction snapshot/tail framing but deliberately defers
// interaction response, reconnect recovery beyond fencing, suspend/resume, and
// snapshot semantics to their owning suites. Run owns its sessions and streams,
// joins every cleanup error into its result, and never closes the Connector.
package conformance
