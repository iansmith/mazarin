// client.go — real implementation of mazarin/httpclient.New + Do.
//
// New() builds a *client with the applied options and validated config;
// the underlying uring dispatcher + protocol-http SID are resolved
// lazily on the first Do() call so the constructor stays kernel-free.
//
// Do() validates the request shape up-front, ShareRanges the caller's
// Body and respDest Slabs to the protocol-http shepherd, encodes a
// HttpDoReqPayload, and blocks on the response channel. The Slabs
// remain owned by the caller throughout; protocol-http receives only
// R/W mappings and emits a one-way Release when done.
package httpclient

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"mazzy/mazarin/sys"
	"mazzy/mazarin/transfer"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
)

// defaultShepherdName is the registry key protocol-http registers under.
const defaultShepherdName = "protocol-http"

// defaultRespRing is the ring index the client uses for inbound
// HttpDoResp + ShareRelease messages from protocol-http. Ring 0 is
// reserved for whatever the caller's main IPC traffic looks like; the
// client picks a dedicated ring to avoid head-of-line blocking. v1
// caps at one client per process; future per-instance ring assignment
// would surface as a WithRespRing option.
const defaultRespRing = 3

// defaultDoTimeout bounds how long Do will wait for a response from
// protocol-http before giving up. TLS handshake + request + response
// for the Anthropic case runs ~1–3s on a healthy link; 30s is roomy.
const defaultDoTimeout = 30 * time.Second

// New constructs an HttpProtocolClient backed by the protocol-http
// shepherd. Returns an error if any required option is missing.
func New(opts ...Option) (HttpProtocolClient, error) {
	cfg := &config{
		shepherdName:  defaultShepherdName,
		minTLSVersion: 0x0303, // tls.VersionTLS12
	}
	for _, o := range opts {
		o(cfg)
	}
	// Note: WithRootCAs is intentionally NOT required in v1. The actual
	// TLS handshake happens inside protocol-http against its own pool
	// loaded from /protocol-http/ssl/cacert.pem; the client's pool is
	// not yet plumbed over IPC. Keep the option for forward compat —
	// future "per-request trust anchors" feature will use it.
	if !cfg.endpointIPSet || cfg.endpointHost == "" {
		return nil, errors.New("httpclient: WithEndpointIP is required (DNS lands in MAZ-41)")
	}
	if cfg.shepherdName == "" {
		return nil, errors.New("httpclient: WithShepherdName('') is not allowed")
	}
	if len(cfg.endpointHost) > ipc.HttpHostMaxInline {
		return nil, fmt.Errorf("httpclient: endpoint host %q exceeds %d-byte inline cap",
			cfg.endpointHost, ipc.HttpHostMaxInline)
	}
	return &client{cfg: cfg, pending: make(map[uint32]chan *ipc.HttpDoRespPayload)}, nil
}

// client implements HttpProtocolClient.
type client struct {
	cfg *config

	// initOnce gates the lazy dispatcher + SID resolution. Runs on the
	// first Do call so New stays kernel-free.
	initOnce  sync.Once
	initErr   error
	httpSID   transfer.ShepherdID
	netSID    transfer.ShepherdID
	disp      *uring.Dispatcher

	// pending correlates ReqIDs with response-channel waiters. Each Do
	// allocates a fresh channel here; handleHttpDoResp routes the
	// response payload to the right channel.
	pendingMu sync.Mutex
	pending   map[uint32]chan *ipc.HttpDoRespPayload

	reqIDNext uint32
}

func (c *client) Do(req *Request, respDest *transfer.Slab) (*Response, error) {
	if err := c.validate(req, respDest); err != nil {
		return nil, err
	}
	if err := c.ensureInit(); err != nil {
		return nil, err
	}

	// Allocate a fresh ReqID and register a response channel.
	reqID := c.nextReqID()
	respCh := make(chan *ipc.HttpDoRespPayload, 1)
	c.pendingMu.Lock()
	c.pending[reqID] = respCh
	c.pendingMu.Unlock()
	defer c.deleteReqID(reqID)

	// Grant protocol-http R/W into the caller's Slabs. v1 shares the
	// full Slabs (full prefix + body region; full pre-posted region).
	// MAZ-56 will tighten ShareID correlation; v1 relies on arrival-
	// order pairing on the protocol-http side.
	reqShareID, err := req.Body.ShareRange(c.httpSID, 0, len(req.Body.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("httpclient: ShareRange(req body): %w", err)
	}
	respShareID, err := respDest.ShareRange(c.httpSID, 0, len(respDest.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("httpclient: ShareRange(respDest): %w", err)
	}

	// Extract the path component of the URL and write it into the start
	// of the request Slab. The URL travels in the Slab (not inline in
	// the IPC) so paths longer than any fixed-size inline cap work.
	urlPath := extractURLPath(req.URL)
	method, ok := methodToEnum(req.Method)
	if !ok {
		return nil, fmt.Errorf("httpclient: unsupported method %q", req.Method)
	}
	// The URL must fit in the prefix region before the body starts.
	// protocol-http will overwrite some of those bytes with the
	// right-aligned headers, but it knows to read the URL from offset
	// 0..URLLen first.
	if len(urlPath) > req.HeadersMax {
		return nil, fmt.Errorf("httpclient: URL path %d bytes exceeds HeadersMax=%d (caller must reserve more prefix room)",
			len(urlPath), req.HeadersMax)
	}
	bodyBytes := req.Body.Bytes()
	copy(bodyBytes[:len(urlPath)], urlPath)

	payload := ipc.HttpDoReqPayload{
		Method:           method,
		URLLen:           uint16(len(urlPath)),
		ReqShareID:       uint32(reqShareID),
		RespShareID:      uint32(respShareID),
		HeadersMaxOffset: uint32(req.HeadersMax),
		ReqBodyLen:       uint32(req.BodyLen),
		ReqID:            reqID,
		RespRing:         uint8(defaultRespRing),
		HostLen:          uint8(len(c.cfg.endpointHost)),
		EndpointFamily:   ipc.HttpAddrIPv4, // v6 lands when MAZ-33 widens netproto.Addr
		EndpointPort:     443,              // HTTPS only in v1
	}
	copy(payload.EndpointAddr[:4], c.cfg.endpointIP[:])
	copy(payload.Host[:], c.cfg.endpointHost)

	msg := ipc.EncodeHttpDoReq(&payload, int16(os.Getpid()))
	if err := uring.Send(int(c.httpSID), &msg); err != nil {
		return nil, fmt.Errorf("httpclient: uring.Send: %w", err)
	}

	// Block on the response with a timeout.
	select {
	case resp := <-respCh:
		return mapResp(resp, respDest)
	case <-time.After(defaultDoTimeout):
		return nil, fmt.Errorf("httpclient: Do timed out after %s", defaultDoTimeout)
	}
}

// ensureInit resolves the protocol-http SID, sets up the dedicated
// response ring, and wires the dispatcher. Runs at most once via
// sync.Once; subsequent calls reuse the established state.
func (c *client) ensureInit() error {
	c.initOnce.Do(func() {
		sid, err := sys.GetShepherdByName(c.cfg.shepherdName)
		if err != nil {
			c.initErr = fmt.Errorf("httpclient: GetShepherdByName(%q): %w", c.cfg.shepherdName, err)
			return
		}
		c.httpSID = transfer.ShepherdID(sid)

		if err := uring.Setup(defaultRespRing); err != nil {
			c.initErr = fmt.Errorf("httpclient: uring.Setup(%d): %w", defaultRespRing, err)
			return
		}

		c.disp = uring.NewDispatcherWithRing(defaultRespRing)
		c.disp.OnFunc(ipc.ProtoHttpIPCResp, decodeHttpDoResp, c.handleHttpDoResp)
		transfer.RegisterShareRelease(c.disp)
		c.disp.Start()
	})
	return c.initErr
}

func decodeHttpDoResp(msg *ipc.UringIPCMsg) any {
	// Copy out — the underlying ring slot may be overwritten after the
	// dispatch loop hands us this pointer.
	cp := *ipc.DecodeHttpDoResp(msg)
	return &cp
}

func (c *client) handleHttpDoResp(v any) {
	resp, ok := v.(*ipc.HttpDoRespPayload)
	if !ok {
		return
	}
	c.pendingMu.Lock()
	ch, ok := c.pending[resp.ReqID]
	c.pendingMu.Unlock()
	if !ok {
		return // unknown ReqID — either the caller timed out or it's a stale duplicate
	}
	// Non-blocking send: the channel is buffered to size 1, so this
	// always succeeds for the legitimate waiter. If the channel is
	// already full (shouldn't happen — one resp per ReqID), drop.
	select {
	case ch <- resp:
	default:
	}
}

func (c *client) deleteReqID(id uint32) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *client) nextReqID() uint32 {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	c.reqIDNext++
	return c.reqIDNext
}

// mapResp translates a HttpDoRespPayload into a *Response, surfacing
// any error code carried in payload.Err.
func mapResp(p *ipc.HttpDoRespPayload, respDest *transfer.Slab) (*Response, error) {
	switch p.Err {
	case ipc.HttpDoOK:
		// Parse headers on this side: protocol-http only reports the byte
		// bounds; the headers themselves live in respDest. v1 returns
		// nil for Response.Headers — callers that need them parse
		// respDest.Bytes()[:HeaderEnd] locally. Tight if not pretty;
		// the alternative is an extra Slab for parsed headers.
		return &Response{
			StatusCode: int(p.StatusCode),
			HeaderEnd:  int(p.HeaderEnd),
			BodyLen:    int(p.BodyLen),
		}, nil
	case ipc.HttpDoErrRespTooBig:
		return nil, fmt.Errorf("httpclient: response exceeds pre-posted Slab capacity (need %d more bytes)", p.BytesNeeded)
	case ipc.HttpDoErrConnect:
		return nil, errors.New("httpclient: TCP connect failed")
	case ipc.HttpDoErrTLS:
		return nil, errors.New("httpclient: TLS handshake/record failed")
	case ipc.HttpDoErrParse:
		return nil, errors.New("httpclient: HTTP/1.1 response parse failed")
	case ipc.HttpDoErrTimeout:
		return nil, errors.New("httpclient: protocol-http reported timeout")
	default:
		return nil, fmt.Errorf("httpclient: protocol-http reported error code %d", p.Err)
	}
}

// methodToEnum maps an HTTP method string to its IPC enum value.
func methodToEnum(m string) (ipc.HttpMethod, bool) {
	switch m {
	case "GET":
		return ipc.HttpMethodGET, true
	case "POST":
		return ipc.HttpMethodPOST, true
	case "PUT":
		return ipc.HttpMethodPUT, true
	case "DELETE":
		return ipc.HttpMethodDELETE, true
	case "HEAD":
		return ipc.HttpMethodHEAD, true
	case "PATCH":
		return ipc.HttpMethodPATCH, true
	default:
		return ipc.HttpMethodInvalid, false
	}
}

// extractURLPath strips "scheme://host" from a full URL, returning just
// the path-and-query portion (or "/" if the URL is bare). We hand-roll
// to avoid pulling in net/url's transitive deps; v1 only needs the
// common "https://host[:port]/path" case.
func extractURLPath(rawURL string) string {
	// Find "://" — if absent, treat the whole thing as a path.
	const schemeSep = "://"
	i := indexOf(rawURL, schemeSep)
	if i < 0 {
		if rawURL == "" {
			return "/"
		}
		return rawURL
	}
	// Skip the scheme + separator; find the first '/' after that.
	rest := rawURL[i+len(schemeSep):]
	j := indexByte(rest, '/')
	if j < 0 {
		return "/"
	}
	return rest[j:]
}

func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func indexByte(s string, b byte) int {
	for i := range len(s) {
		if s[i] == b {
			return i
		}
	}
	return -1
}

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
