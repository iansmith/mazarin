package main

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"strings"
)

// extractBodyVariants parses one mbox message's raw body and returns the
// decoded text/plain and text/html parts. Inputs:
//
//   - headers: case-folded header map from the mbox parser (keys are lowercased)
//   - body:    raw body bytes (everything after the outer header blank line)
//
// For non-multipart messages, the outer Content-Type/Content-Transfer-Encoding
// determine which variant the body bytes go into. For multipart messages, the
// body is walked recursively; the LAST text/plain and LAST text/html parts win
// (matches multipart/alternative semantics where richer alternatives appear last).
//
// Both return values may be empty strings if the corresponding variant isn't
// present in the message.
func extractBodyVariants(headers map[string]string, body string) (text, html string) {
	ctype := headers["content-type"]
	if ctype == "" {
		// No Content-Type — treat as 7bit text/plain.
		return body, ""
	}

	mediaType, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		// Malformed Content-Type — fall back to treating the body as plain text.
		return body, ""
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return body, ""
		}
		return walkMultipart(strings.NewReader(body), boundary)
	}

	// Single-part message. Decode based on Content-Transfer-Encoding.
	cte := strings.ToLower(headers["content-transfer-encoding"])
	decoded := decodeBody(body, cte)

	switch {
	case strings.HasPrefix(mediaType, "text/html"):
		return "", decoded
	case strings.HasPrefix(mediaType, "text/"):
		return decoded, ""
	}
	// Non-text top-level content — ignore for body extraction purposes.
	return "", ""
}

// walkMultipart consumes a multipart MIME body and accumulates the LAST
// text/plain and text/html parts found at any nesting depth. Nested
// multiparts are walked recursively.
func walkMultipart(r io.Reader, boundary string) (text, html string) {
	mr := multipart.NewReader(r, boundary)
	for {
		part, err := mr.NextPart()
		if err != nil {
			return
		}
		ctype := part.Header.Get("Content-Type")
		if ctype == "" {
			ctype = "text/plain"
		}
		mediaType, params, mErr := mime.ParseMediaType(ctype)
		if mErr != nil {
			part.Close()
			continue
		}

		if strings.HasPrefix(mediaType, "multipart/") {
			nestedBoundary := params["boundary"]
			if nestedBoundary != "" {
				nText, nHTML := walkMultipart(part, nestedBoundary)
				if nText != "" {
					text = nText
				}
				if nHTML != "" {
					html = nHTML
				}
			}
			part.Close()
			continue
		}

		// Read the part body.
		buf, rErr := io.ReadAll(part)
		part.Close()
		if rErr != nil {
			continue
		}

		cte := strings.ToLower(part.Header.Get("Content-Transfer-Encoding"))
		decoded := decodeBody(string(buf), cte)

		switch {
		case strings.HasPrefix(mediaType, "text/html"):
			html = decoded
		case strings.HasPrefix(mediaType, "text/"):
			text = decoded
		}
	}
}

// decodeBody applies the Content-Transfer-Encoding to raw body bytes.
// Supports quoted-printable, base64, and the identity encodings (7bit, 8bit,
// binary, or unset).
func decodeBody(body, encoding string) string {
	switch encoding {
	case "quoted-printable":
		dec, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
		if err != nil {
			return body
		}
		return string(dec)
	case "base64":
		// Base64 in MIME is line-wrapped; the standard decoder handles \r\n
		// in the input via its own buffering, but to be safe strip whitespace.
		clean := stripWhitespace(body)
		dec, err := base64.StdEncoding.DecodeString(clean)
		if err != nil {
			// Try URL-safe as a fallback for robustness against quirky senders.
			dec, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "="))
			if err != nil {
				return body
			}
		}
		return string(dec)
	}
	return body
}

func stripWhitespace(s string) string {
	var b bytes.Buffer
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
