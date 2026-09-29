package httpclient

import (
	"net/http"
	"time"
)

// apiTransport is shared by every NewAPI client so that they pool
// connections, as clients on http.DefaultTransport did. It starts from
// DefaultTransport, so HTTP/2 stays on, and it adds the proxy rules of
// Streaming. It has no ICY handling.
var apiTransport = &socks5RoundTripper{transport: newAPITransport()}

func newAPITransport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = transportProxy
	tr.DialContext = dialWithDecision
	return tr
}

// NewAPI returns a client for API, auth and download requests. timeout limits
// each request, including the body read. The client honors HTTP_PROXY,
// HTTPS_PROXY, ALL_PROXY and NO_PROXY like Streaming, and it sends UserAgent
// when a request has no User-Agent header.
func NewAPI(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: userAgentTransport{base: apiTransport},
	}
}

// userAgentTransport sets UserAgent on a request that has no User-Agent
// header. A request that sets its own value, or an empty value to send none,
// keeps it.
type userAgentTransport struct {
	base http.RoundTripper
}

func (t userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if _, ok := req.Header["User-Agent"]; ok {
		return t.base.RoundTrip(req)
	}
	// A RoundTripper must not change the request it gets, so set the
	// header on a copy.
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", UserAgent)
	return t.base.RoundTrip(req)
}
