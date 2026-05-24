// Package claude is a minimal client for the Anthropic Messages API.
// Scope: just enough to support sophie.maz's MAZ-43 smoke test —
// one-shot Ask, one model, one prompt, one substring assertion on the
// response. No streaming, no tools, no system prompts, no caching, no
// retry, no token accounting. See MAZ-43 for the rationale on what
// shipped and what didn't.
package claude

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
)

// anthropicHost is the Messages API hostname. Hard-coded because there's
// exactly one Anthropic API endpoint and sophie has no reason to talk to
// anything else.
const anthropicHost = "api.anthropic.com"

// anthropicMessagesURL is the one endpoint we POST to. The hostname is
// duplicated with anthropicHost intentionally — both are referenced
// independently (one in tls.Config.ServerName, one in the HTTP request
// URL) and a const is clearer than a Sprintf-or-concat at the call sites.
const anthropicMessagesURL = "https://api.anthropic.com/v1/messages"

// anthropicVersion is the API version we pin to. Anthropic versions the
// wire shape; pinning means we don't get surprised by future changes.
const anthropicVersion = "2023-06-01"

// maxTokens caps the response length. 1024 is plenty for sophie's
// "Washington" assertion plus a sentence or two of context. Inlined into
// encodeMessagesRequest rather than parameterized — only one call site.
const maxTokens = 1024

// Client speaks the Anthropic Messages API. Holds the API key, model
// name, and a pre-built *tls.Config so repeated Asks don't re-allocate.
// Not safe for concurrent Ask calls — caller serializes.
type Client struct {
	apiKey string
	model  string
	tlsCfg *tls.Config
}

// NewClient constructs a Client. apiKey must be non-empty; caPool must
// be non-nil (the caller — sophie — parses the PEM bundle and supplies
// the pool). model is the Claude model identifier to request, e.g.
// "claude-opus-4-5".
func NewClient(apiKey, model string, caPool *x509.CertPool) (*Client, error) {
	if apiKey == "" {
		return nil, errors.New("claude: empty API key")
	}
	if model == "" {
		return nil, errors.New("claude: empty model")
	}
	if caPool == nil {
		return nil, errors.New("claude: nil CA pool")
	}
	return &Client{
		apiKey: apiKey,
		model:  model,
		tlsCfg: &tls.Config{
			ServerName: anthropicHost,
			RootCAs:    caPool,
			MinVersion: tls.VersionTLS12,
			// Pin HTTP/1.1 via ALPN. Otherwise modern servers happily negotiate
			// HTTP/2 and our hand-rolled HTTP/1 parser falls over.
			NextProtos: []string{"http/1.1"},
		},
	}, nil
}

// TLSConfig returns the prepared *tls.Config. Exported so tests can
// inspect the config-shape invariants and so sophie can use it directly
// for tls.Client(...) without re-allocating.
func (c *Client) TLSConfig() *tls.Config {
	return c.tlsCfg
}

// Ask sends prompt as a one-shot user message and returns the
// assistant's text response. conn is a raw transport-level net.Conn
// (typically sophie's adapter over netclient); Ask wraps it with TLS,
// completes the handshake, writes the HTTP/1.1 POST, reads the
// response, and parses out the assistant's text.
//
// The caller owns conn's lifecycle — Ask does NOT close it. The deferred
// CloseWrite sends TLS close_notify on the write side of tlsConn without
// closing the underlying conn (unlike tlsConn.Close, which would).
func (c *Client) Ask(conn net.Conn, prompt string) (string, error) {
	tlsConn := tls.Client(conn, c.tlsCfg)
	// Explicit handshake — better than implicit on first Write/Read so
	// handshake errors don't get tangled with application-protocol I/O.
	if err := tlsConn.Handshake(); err != nil {
		return "", fmt.Errorf("claude: TLS handshake: %w", err)
	}
	defer tlsConn.CloseWrite()

	body, err := encodeMessagesRequest(c.model, prompt)
	if err != nil {
		return "", fmt.Errorf("claude: encode request: %w", err)
	}

	req, err := http.NewRequest("POST", anthropicMessagesURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("claude: build request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("content-type", "application/json")
	// One TLS connection per Ask — no keep-alive bookkeeping.
	req.Header.Set("connection", "close")

	if err := req.Write(tlsConn); err != nil {
		return "", fmt.Errorf("claude: send request: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), req)
	if err != nil {
		return "", fmt.Errorf("claude: read response: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("claude: drain response: %w", err)
	}

	return parseMessagesResponse(respBody)
}

// encodeMessagesRequest builds the JSON body for POST /v1/messages.
func encodeMessagesRequest(model, prompt string) ([]byte, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type body struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []msg  `json:"messages"`
	}
	return json.Marshal(body{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  []msg{{Role: "user", Content: prompt}},
	})
}

// parseMessagesResponse extracts the assistant's text from a Messages
// API response body. Returns an error for API-error responses (4xx/5xx
// with {"type":"error", "error":{...}}) so the caller doesn't silently
// see empty text on auth failures or rate limits.
func parseMessagesResponse(body []byte) (string, error) {
	type contentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type apiError struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	type respShape struct {
		Type    string         `json:"type"`
		Content []contentBlock `json:"content"`
		Error   *apiError      `json:"error,omitempty"`
	}
	var r respShape
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("claude: unmarshal response: %w", err)
	}
	if r.Error != nil {
		return "", fmt.Errorf("claude: API error (%s): %s", r.Error.Type, r.Error.Message)
	}
	if r.Type == "error" {
		return "", errors.New("claude: API error (no detail)")
	}
	if len(r.Content) == 0 {
		return "", errors.New("claude: response has no content blocks")
	}
	if r.Content[0].Type != "text" {
		return "", fmt.Errorf("claude: first content block is %q, want text", r.Content[0].Type)
	}
	return r.Content[0].Text, nil
}
