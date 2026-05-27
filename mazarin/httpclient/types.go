// Package httpclient is the mazarin-native HTTP/1.1 client surface
// backed by the protocol-http shepherd. See MAZ-49.
//
// The public shape is intentionally narrow:
//
//	client, _ := httpclient.New(
//	    httpclient.WithRootCAs(pool),
//	    httpclient.WithEndpointIP("api.anthropic.com", [4]byte{1,2,3,4}),
//	)
//	resp, err := client.Do(req, respDest)
//
// Both req.Body and respDest are caller-owned *transfer.Slab values.
// httpclient takes a temporary R/W ShareRange grant into the protocol-
// http shepherd's address space for the duration of the call; on
// return, the caller still owns the pages and can re-use them or free
// them. The data plane never transfers ownership (this is the
// share-only / pre-posted-buffer pattern landed by MAZ-53).
//
// Not compatible with Go's net/http. Any compatibility adapter belongs
// in a future linux-emulation layer and is explicitly out of scope.
package httpclient

import "mazzy/mazarin/transfer"

// Header is a single HTTP/1.1 header.
//
// Name and Value are encoded as-is into the wire bytes — the caller is
// responsible for any normalization the target server may require.
type Header struct {
	Name, Value string
}

// Request describes one HTTP/1.1 request to be sent by the protocol-http
// shepherd.
//
// The Body Slab holds [prefix | json body]: bytes [0:HeadersMax] are
// reserved for protocol-http to write request line + headers into
// (right-aligned within the prefix so the headers end exactly at the
// body's first byte, producing one contiguous wire send), and bytes
// [HeadersMax:HeadersMax+BodyLen] are the JSON body the caller wrote.
type Request struct {
	Method     string
	URL        string
	Headers    []Header
	Body       *transfer.Slab
	HeadersMax int // byte offset within Body where the JSON body region starts
	BodyLen    int // actual body length (bytes [HeadersMax:HeadersMax+BodyLen] in Body)
}

// Response is the parsed HTTP/1.1 response returned by Do.
//
// The response body lives in the respDest Slab the caller passed to Do
// — Response does not own those pages, only the parsed metadata.
// Body bytes are at respDest.Bytes()[HeaderEnd : HeaderEnd+BodyLen]
// after Do returns successfully.
type Response struct {
	StatusCode int
	Headers    []Header
	HeaderEnd  int // byte offset within respDest where the body starts
	BodyLen    int // body length within respDest
}

// HttpProtocolClient is the per-shepherd-connection client surface. Do
// is intentionally synchronous + one-shot: connection reuse, streaming
// responses, and HTTP/2 multiplexing are out of scope for v1 (filed as
// MAZ-49-keepalive and MAZ-49-streaming follow-ups).
type HttpProtocolClient interface {
	Do(req *Request, respDest *transfer.Slab) (*Response, error)
}
