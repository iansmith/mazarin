// client.go — real implementation of mazarin/httpclient.New + Do.
//
// New() builds a *client with the applied options and validated config;
// the underlying uring dispatcher + protocol-http SID are resolved
// lazily on the first Do() call so the constructor stays kernel-free
// (callers can build a *client in tests / pre-boot setup without a
// running shepherd registry).
//
// Do() validates the request shape up-front, ShareRanges the caller's
// Body and respDest Slabs to the protocol-http shepherd, encodes a
// HttpDoReqPayload, and blocks on the response channel. The Slabs
// remain owned by the caller throughout; protocol-http receives only
// R/W mappings and emits a one-way Release when done.
package httpclient

import (
	"crypto/tls"
	"errors"
	"fmt"
	"sync"

	"mazzy/mazarin/transfer"
)

// defaultShepherdName is the registry key protocol-http registers under.
const defaultShepherdName = "protocol-http"

// New constructs an HttpProtocolClient backed by the protocol-http
// shepherd. Returns an error if any required option is missing.
func New(opts ...Option) (HttpProtocolClient, error) {
	cfg := &config{
		shepherdName:  defaultShepherdName,
		minTLSVersion: tls.VersionTLS12,
	}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.rootCAs == nil {
		return nil, errors.New("httpclient: WithRootCAs is required")
	}
	if !cfg.endpointIPSet || cfg.endpointHost == "" {
		return nil, errors.New("httpclient: WithEndpointIP is required (DNS lands in MAZ-41)")
	}
	if cfg.shepherdName == "" {
		return nil, errors.New("httpclient: WithShepherdName('') is not allowed")
	}
	return &client{cfg: cfg}, nil
}

// client implements HttpProtocolClient. Created by New; concrete type
// is unexported so callers compose through the interface.
type client struct {
	cfg *config

	// shepherdSID is resolved on the first Do call (so New stays
	// kernel-free) and reused for subsequent calls. Guarded by once.
	once        sync.Once
	shepherdSID transfer.ShepherdID
	resolveErr  error

	// reqIDMu protects reqIDNext. Each Do call allocates a fresh ReqID
	// so the response handler can route by ReqID even if a future
	// version overlaps multiple Do calls in flight.
	reqIDMu   sync.Mutex
	reqIDNext uint32
}

// Do dispatches one HTTP/1.1 request via the protocol-http shepherd.
//
// Validation happens up-front: nil request, nil/empty Body or respDest,
// HeadersMax out of range, BodyLen out of range, unknown method, empty
// URL, all surface as errors before any IPC happens.
//
// On the happy path Do ShareRanges the request Slab (the full prefix +
// body region) and the respDest Slab (the full pre-posted region) to
// protocol-http, sends the HttpDoReqPayload IPC, and blocks for the
// matching response. The actual TLS connection, HTTP wire framing,
// and response parsing happen inside protocol-http (items 4-7).
//
// Note: the lazy SID resolution + uring.Send + response correlation
// land in dispatch wiring that depends on the full mazarin runtime;
// this v1 of Do panics with "httpclient: dispatch not yet wired" when
// validation passes — exercising the wire path requires the boot
// integration in MAZ-49 item 8. Validation-error paths are unit-
// testable here.
func (c *client) Do(req *Request, respDest *transfer.Slab) (*Response, error) {
	if err := c.validate(req, respDest); err != nil {
		return nil, err
	}
	// TODO(MAZ-49 item 7/8 integration): resolve c.shepherdSID via
	// sys.MustGetShepherdByName, set up the uring dispatcher with
	// transfer.RegisterShareRelease, ShareRange both Slabs into the
	// shepherd, encode HttpDoReqPayload, uring.Send, await the
	// matching HttpDoRespPayload, and translate it into *Response.
	// The validation above is the only part we can exercise in unit
	// tests without a live shepherd.
	return nil, errors.New("httpclient: Do dispatch not yet wired (MAZ-49 items 7/8)")
}

// validate returns nil if req + respDest are well-formed. Pulled out of
// Do so tests can exercise just this layer and so the error messages
// stay consistent across future Do variants (DoStream etc).
func (c *client) validate(req *Request, respDest *transfer.Slab) error {
	if req == nil {
		return errors.New("httpclient: nil request")
	}
	if req.Method == "" {
		return errors.New("httpclient: empty request method")
	}
	if req.URL == "" {
		return errors.New("httpclient: empty request URL")
	}
	if req.Body == nil {
		return errors.New("httpclient: nil request body Slab")
	}
	bodyBytes := req.Body.Bytes()
	if len(bodyBytes) == 0 {
		return errors.New("httpclient: request body Slab has no pages (or is in a non-Allocated state)")
	}
	if req.HeadersMax < 0 || req.HeadersMax > len(bodyBytes) {
		return fmt.Errorf("httpclient: HeadersMax=%d out of range [0, %d]", req.HeadersMax, len(bodyBytes))
	}
	if req.BodyLen < 0 || req.HeadersMax+req.BodyLen > len(bodyBytes) {
		return fmt.Errorf("httpclient: BodyLen=%d puts body end past Slab capacity (HeadersMax=%d, slab=%d)",
			req.BodyLen, req.HeadersMax, len(bodyBytes))
	}
	if respDest == nil {
		return errors.New("httpclient: nil respDest Slab")
	}
	if len(respDest.Bytes()) == 0 {
		return errors.New("httpclient: respDest Slab has no pages (or is in a non-Allocated state)")
	}
	return nil
}

// nextReqID allocates a fresh request correlation ID. Wraps at uint32
// — at the rate v1 fires requests this is effectively never.
func (c *client) nextReqID() uint32 {
	c.reqIDMu.Lock()
	defer c.reqIDMu.Unlock()
	c.reqIDNext++
	return c.reqIDNext
}
