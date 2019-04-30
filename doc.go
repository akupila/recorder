// Package recorder provides an HTTP record/replay transport.
//
// The primary use-case is for tests where HTTP requests are sent and can be
// replayed without needing to reach out to the network. The Recorder is
// configurable to always perform request, never perform requests, or auto,
// where requests are performed when no existing entry exists.
package recorder
