// Package kas is a minimal client for All-Inkl's KAS API, covering only the
// DNS record operations an ACME DNS-01 solver needs.
//
// The API is "SOAP" in shape only: there is exactly one operation (KasApi),
// and its single <Params> element carries a JSON document describing what you
// actually want. That means no WSDL worth generating code from and no SOAP
// library -- a literal envelope template plus encoding/json covers the
// request side entirely. Only the response needs real XML work, because it
// comes back as SOAP-encoded Maps and Arrays rather than a flat structure.
package kas

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiEndpoint = "https://kasapi.kasserver.com/soap/KasApi.php"
	soapAction  = "urn:xmethodsKasApi#KasApi"

	// %s is the JSON request document. Kept as a template rather than built
	// with encoding/xml because the envelope never varies.
	envelopeTemplate = `<?xml version="1.0" encoding="utf-8"?>` +
		`<soap-env:Envelope xmlns:soap-env="http://schemas.xmlsoap.org/soap/envelope/">` +
		`<soap-env:Body>` +
		`<ns0:KasApi xmlns:ns0="urn:xmethodsKasApi"><Params>%s</Params></ns0:KasApi>` +
		`</soap-env:Body></soap-env:Envelope>`

	// Used when the API says it is flood-protected but the delay it reports
	// cannot be parsed. Deliberately generous: being slow is fine here,
	// hammering a shared hosting API is not.
	defaultFloodDelay = 5 * time.Second
)

// Client talks to the KAS API as one account. Safe for concurrent use.
type Client struct {
	User     string
	Password string

	// HTTPClient is optional; a 30s-timeout client is used when nil.
	HTTPClient *http.Client

	// MaxRetries bounds flood-protection retries per call. Zero means 5.
	MaxRetries int

	// KAS reports, on every successful response, how long the caller must
	// wait before the next request (KasFloodDelay). Honouring it proactively
	// is what keeps a Present/CleanUp pair from tripping flood protection on
	// its own second call, which is the failure mode that makes naive
	// clients look flaky.
	mu       sync.Mutex
	nextFree time.Time
}

// Record is one DNS record as KAS reports it.
type Record struct {
	ID   string
	Zone string
	Name string
	Type string
	Data string
	Aux  string
}

// FloodError reports that the API refused a call for rate-limiting reasons.
// It is retried internally; it only escapes once MaxRetries is exhausted.
type FloodError struct{ Delay time.Duration }

func (e *FloodError) Error() string {
	return fmt.Sprintf("kas: flood protection active, retry after %s", e.Delay)
}

// APIError is a non-flood fault from the API.
type APIError struct{ Code, Message string }

func (e *APIError) Error() string {
	if e.Code == "" {
		return "kas: " + e.Message
	}
	return fmt.Sprintf("kas: %s (%s)", e.Message, e.Code)
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) maxRetries() int {
	if c.MaxRetries <= 0 {
		return 5
	}
	return c.MaxRetries
}

// waitTurn blocks until the flood delay reported by the previous response has
// elapsed.
func (c *Client) waitTurn(ctx context.Context) error {
	c.mu.Lock()
	wait := time.Until(c.nextFree)
	c.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	return sleepCtx(ctx, wait)
}

func (c *Client) noteFloodDelay(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextFree = time.Now().Add(d)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// call performs one KAS request, retrying while the API reports flood
// protection. The returned value is the decoded "Response" map.
func (c *Client) call(ctx context.Context, requestType string, params map[string]string) (map[string]any, error) {
	body, err := json.Marshal(map[string]any{
		"KasUser":          c.User,
		"KasAuthType":      "plain",
		"KasAuthData":      c.Password,
		"KasRequestType":   requestType,
		"KasRequestParams": params,
	})
	if err != nil {
		return nil, fmt.Errorf("kas: encoding request: %w", err)
	}

	// The JSON document is placed inside an XML element, so it must be
	// XML-escaped first.
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, body); err != nil {
		return nil, fmt.Errorf("kas: escaping request: %w", err)
	}
	envelope := fmt.Sprintf(envelopeTemplate, escaped.String())

	var lastErr error
	for attempt := 0; attempt < c.maxRetries(); attempt++ {
		if err := c.waitTurn(ctx); err != nil {
			return nil, err
		}

		resp, err := c.do(ctx, envelope)
		if err == nil {
			return resp, nil
		}

		var flood *FloodError
		if !errors.As(err, &flood) {
			return nil, err
		}
		lastErr = err
		c.noteFloodDelay(flood.Delay)
	}
	return nil, lastErr
}

func (c *Client) do(ctx context.Context, envelope string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiEndpoint, strings.NewReader(envelope))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", soapAction)

	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("kas: calling API: %w", err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("kas: reading response: %w", err)
	}

	// A SOAP fault arrives with HTTP 500, so the status code alone is not a
	// usable signal -- parse the body first and only fall back to status.
	var env soapEnvelope
	if err := xml.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("kas: parsing response (HTTP %d): %w", res.StatusCode, err)
	}

	if f := env.Body.Fault; f != nil {
		msg := strings.TrimSpace(f.FaultString)
		if delay, ok := floodDelayFrom(msg, decodeNode(f.Detail)); ok {
			return nil, &FloodError{Delay: delay}
		}
		return nil, &APIError{Code: strings.TrimSpace(f.FaultCode), Message: msg}
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kas: unexpected HTTP %d", res.StatusCode)
	}
	if len(env.Body.Content) == 0 {
		return nil, fmt.Errorf("kas: empty response body")
	}

	decoded, _ := decodeNode(env.Body.Content[0]).(map[string]any)
	response, _ := decoded["Response"].(map[string]any)
	if response == nil {
		return nil, fmt.Errorf("kas: response contained no Response map")
	}

	// Successful responses carry the delay the caller must observe before
	// issuing the next request.
	if d, ok := asFloat(response["KasFloodDelay"]); ok && d > 0 {
		c.noteFloodDelay(time.Duration(d * float64(time.Second)))
	}
	if s, ok := response["ReturnString"].(string); ok && !strings.EqualFold(s, "TRUE") {
		return nil, &APIError{Message: "API returned " + s}
	}
	return response, nil
}

// GetRecords lists every record in a zone. zone must be the KAS zone host,
// including its trailing dot, e.g. "example.com.".
func (c *Client) GetRecords(ctx context.Context, zone string) ([]Record, error) {
	resp, err := c.call(ctx, "get_dns_settings", map[string]string{"zone_host": zone})
	if err != nil {
		return nil, err
	}
	entries, _ := resp["ReturnInfo"].([]any)
	records := make([]Record, 0, len(entries))
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		records = append(records, Record{
			ID:   str(m["record_id"]),
			Zone: str(m["record_zone"]),
			Name: str(m["record_name"]),
			Type: str(m["record_type"]),
			Data: str(m["record_data"]),
			Aux:  str(m["record_aux"]),
		})
	}
	return records, nil
}

// AddTXTRecord creates a TXT record and returns its KAS record id. KAS always
// appends rather than replacing, so several records may share a name -- which
// is exactly what a wildcard-plus-apex certificate needs.
func (c *Client) AddTXTRecord(ctx context.Context, zone, name, data string) (string, error) {
	resp, err := c.call(ctx, "add_dns_settings", map[string]string{
		"zone_host":   zone,
		"record_name": name,
		"record_type": "TXT",
		"record_data": data,
		"record_aux":  "0",
	})
	if err != nil {
		return "", err
	}
	return str(resp["ReturnInfo"]), nil
}

// DeleteRecord removes one record by its KAS record id.
func (c *Client) DeleteRecord(ctx context.Context, recordID string) error {
	_, err := c.call(ctx, "delete_dns_settings", map[string]string{"record_id": recordID})
	return err
}

// --- response decoding -----------------------------------------------------

type soapEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		Fault *struct {
			FaultCode   string  `xml:"faultcode"`
			FaultString string  `xml:"faultstring"`
			Detail      xmlNode `xml:"detail"`
		} `xml:"Fault"`
		Content []xmlNode `xml:",any"`
	} `xml:"Body"`
}

type xmlNode struct {
	XMLName  xml.Name
	Content  string    `xml:",chardata"`
	Children []xmlNode `xml:",any"`
}

// decodeNode turns SOAP-encoded Maps and Arrays into plain Go values:
// map[string]any for <item><key/><value/></item> sequences, []any for keyless
// <item> sequences, and trimmed strings for leaves.
func decodeNode(n xmlNode) any {
	var items []xmlNode
	for _, c := range n.Children {
		if c.XMLName.Local == "item" {
			items = append(items, c)
		}
	}

	if len(items) == 0 {
		switch len(n.Children) {
		case 0:
			return strings.TrimSpace(n.Content)
		case 1:
			// Unwrap single-child wrappers such as <return> around a Map.
			return decodeNode(n.Children[0])
		default:
			m := make(map[string]any, len(n.Children))
			for _, c := range n.Children {
				m[c.XMLName.Local] = decodeNode(c)
			}
			return m
		}
	}

	keyed := true
	for _, it := range items {
		if child(it, "key") == nil {
			keyed = false
			break
		}
	}

	if keyed {
		m := make(map[string]any, len(items))
		for _, it := range items {
			k := child(it, "key")
			v := child(it, "value")
			if k == nil {
				continue
			}
			name := strings.TrimSpace(k.Content)
			if v == nil {
				m[name] = ""
				continue
			}
			m[name] = decodeNode(*v)
		}
		return m
	}

	out := make([]any, 0, len(items))
	for _, it := range items {
		out = append(out, decodeNode(it))
	}
	return out
}

func child(n xmlNode, local string) *xmlNode {
	for i := range n.Children {
		if n.Children[i].XMLName.Local == local {
			return &n.Children[i]
		}
	}
	return nil
}

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func asFloat(v any) (float64, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// floodDelayFrom recognises the flood-protection fault and digs the retry
// delay out of whatever shape the detail field arrived in.
func floodDelayFrom(faultString string, detail any) (time.Duration, bool) {
	if !strings.Contains(strings.ToLower(faultString), "flood") {
		return 0, false
	}
	if d, ok := firstFloat(detail); ok && d > 0 {
		return time.Duration(d * float64(time.Second)), true
	}
	return defaultFloodDelay, true
}

func firstFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case string:
		return asFloat(t)
	case map[string]any:
		for _, item := range t {
			if f, ok := firstFloat(item); ok {
				return f, true
			}
		}
	case []any:
		for _, item := range t {
			if f, ok := firstFloat(item); ok {
				return f, true
			}
		}
	}
	return 0, false
}
