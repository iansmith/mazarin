// Package httpclient is the mazarin-native HTTP/1.1 client surface backed by
// the protocol-http shepherd. See MAZ-49.
//
// This file is the planned public surface. The implementation lands in
// client.go / options.go and is filled in over the course of MAZ-49.
package httpclient

import "mazzy/mazarin/transfer"

// Header is a single HTTP/1.1 header.
//
// The Name and Value fields are encoded as-is into the request line — the
// caller is responsible for any normalization (case, trimming) the target
// server may require.
type Header struct {
	Name, Value string
}

// Request describes one HTTP/1.1 request to be sent by the protocol-http
// shepherd.
//
// Body is a committed transfer.Handle owned by the caller's address space
// up to the moment the request is dispatched; on Do() it is handed to
// protocol-http for the wire send.
type Request struct {
	Method  string
	URL     string
	Headers []Header
	Body    transfer.Handle
}

// Response is the parsed HTTP/1.1 response returned by Do().
//
// The response body lives in the respDest the caller passed to Do() —
// Response does not own those pages, only the metadata.
type Response struct {
	StatusCode int
	Headers    []Header
	BodyLen    int
}

// HttpProtocolClient is the per-shepherd-connection client surface.
//
// Do is intentionally synchronous and one-shot: connection reuse is out of
// scope for v1 (MAZ-49 ships Connection: close per request).
type HttpProtocolClient interface {
	Do(req *Request, respDest transfer.Handle) (*Response, error)
}
