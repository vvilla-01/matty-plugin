// Package pushsend delivers Web Push messages (RFC 8030) to the push
// servers phones name, and is the only way the plugin calls an address a
// signed-in person supplies. It confines those calls:
//
//   - a push address is checked when it is registered and before every
//     send (ValidateEndpoint): https, or http only to a push server the
//     admin listed; no user name; a listed push server when only those are
//     allowed;
//   - every connection is checked once the name is resolved, address by
//     address, so that no DNS answer can lead it elsewhere: a push server
//     that is not listed must be at a globally reachable address (not
//     loopback, private, link-local, carrier-grade NAT, documentation,
//     multicast, translation or any other special-purpose range, in any of
//     their IPv4-mapped or embedded spellings); a listed one may be on the
//     admin's own network, but never link-local, multicast, unspecified or
//     at a cloud's metadata address, and on this machine only when the
//     entry itself names it;
//   - the client follows no redirect, uses no proxy from the environment,
//     waits 5 seconds to connect, 5 for TLS and 10 for the whole request,
//     of which at most 1 for the answer's body once its status has come;
//     it reads at most 16 KiB of headers and 4 KiB of the body (which it
//     throws away), takes no answer below 200, and keeps at most 2 idle
//     connections a host;
//   - an outcome carries the push server's host, the status and a kind, and
//     never the address, a key, the message or an error that spells them
//     out; a host given by name whose address the rules refuse comes out
//     as unreachable, as a name that does not resolve does.
//
// Which pushes to send, when to retry and how many at once is the caller's.
package pushsend

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// UserAgent is the User-Agent every push goes with: no version and no
	// address of this server's.
	UserAgent = "MattyPlugin"

	// TTL is how long, in seconds, a push server is asked to keep a push
	// for a phone that is away. Every push is sent with it and with
	// Urgency high: the push server cannot tell them apart.
	TTL = "86400"

	// MaxBody is the most a push server must carry.
	MaxBody = 4096

	// MaxRetryAfter is the longest Retry-After an outcome passes on; a
	// longer one is cut to it.
	MaxRetryAfter = 60 * time.Second

	defaultDialTimeout    = 5 * time.Second
	defaultTLSTimeout     = 5 * time.Second
	defaultRequestTimeout = 10 * time.Second

	answerTimeout          = time.Second
	maxAnswerBytes         = 4 << 10
	maxResponseHeaderBytes = 16 << 10
	maxIdlePerHost         = 2
	maxConnsPerHost        = 4
	idleTimeout            = 60 * time.Second
)

// Result is what became of a push.
type Result int

const (
	// Delivered: the push server took it (any 2xx).
	Delivered Result = iota + 1

	// Gone: the push server says the address is no more (404 or 410).
	Gone

	// Refused: the push server will not take pushes from this server
	// (401 or 403); trying again changes nothing.
	Refused

	// Retry: it may go through later: 429, a 5xx, or no answer (a
	// timeout, a connection that failed, a name that did not resolve or
	// whose addresses the rules refuse). Status is 0 when there was no
	// answer.
	Retry

	// Permanent: it will not go through: any other answer (a redirect, or
	// one below 200 among them), a push address that does not pass under
	// the rules or an IP address they refuse, a certificate that does not
	// verify.
	Permanent
)

func (r Result) String() string {
	switch r {
	case Delivered:
		return "delivered"
	case Gone:
		return "gone"
	case Refused:
		return "refused"
	case Retry:
		return "retry"
	case Permanent:
		return "permanent"
	}

	return "unknown"
}

// Kind says what went wrong, in the words the app shows a person.
type Kind string

const (
	KindNone        Kind = ""
	KindUnreachable Kind = "unreachable" // no answer: a timeout, a connection that failed, a name that led nowhere allowed
	KindAddress     Kind = "address"     // the push address does not pass under the rules, or its IP address is refused
	KindCertificate Kind = "certificate" // the push server's certificate did not verify
	KindAnswer      Kind = "answer"      // the push server answered, but not with a yes
)

// Reasons an outcome gives beside its kind, a fixed vocabulary for logs.
const (
	ReasonStatus      = "status"
	ReasonTimeout     = "timeout"
	ReasonConnection  = "connection"
	ReasonName        = "name lookup"
	ReasonCanceled    = "canceled"
	ReasonRules       = "address rules"
	ReasonCertificate = "certificate"
	ReasonBody        = "message size"
)

// Outcome is what Send came to. Nothing in it is the push address, a key
// or the message.
type Outcome struct {
	Result Result

	// Status is the push server's HTTP status, 0 when it gave none.
	Status int

	// RetryAfter is the push server's Retry-After on a Retry, at most
	// MaxRetryAfter; 0 when it gave none.
	RetryAfter time.Duration

	// Kind is what went wrong; KindNone on Delivered.
	Kind Kind

	// Host is the push server's host as Endpoint.Host gives it.
	Host string

	// Reason is one of the Reason words.
	Reason string
}

// String is the outcome as a log line may carry it.
func (o Outcome) String() string {
	text := o.Result.String()
	if o.Host != "" {
		text += " " + o.Host
	}

	if o.Status != 0 {
		text += " " + strconv.Itoa(o.Status)
	}

	if o.Kind != KindNone {
		text += " " + string(o.Kind)
	}

	if o.Reason != "" && o.Reason != ReasonStatus {
		text += " (" + o.Reason + ")"
	}

	return text
}

// Options set up a Client. Zero values are the defaults.
type Options struct {
	Rules Rules

	// RootCAs are the certificates TLS trusts; nil is the system's.
	RootCAs *x509.CertPool

	// DialTimeout, TLSTimeout and RequestTimeout default to 5 s, 5 s and
	// 10 s.
	DialTimeout    time.Duration
	TLSTimeout     time.Duration
	RequestTimeout time.Duration

	// Now tells the time for an HTTP-date Retry-After; nil is time.Now.
	Now func() time.Time

	// resolver stands in for the system's name lookups in tests.
	resolver *net.Resolver
}

// Client sends pushes. It is safe for use by several goroutines.
type Client struct {
	options Options
	current atomic.Pointer[clientState]
}

type clientState struct {
	rules     Rules
	transport *http.Transport
	http      *http.Client
}

// NewClient is a Client under options.
func NewClient(options Options) *Client {
	if options.DialTimeout <= 0 {
		options.DialTimeout = defaultDialTimeout
	}

	if options.TLSTimeout <= 0 {
		options.TLSTimeout = defaultTLSTimeout
	}

	if options.RequestTimeout <= 0 {
		options.RequestTimeout = defaultRequestTimeout
	}

	if options.Now == nil {
		options.Now = time.Now
	}

	c := &Client{options: options}
	c.current.Store(c.newState(options.Rules))

	return c
}

// Rules are the rules the client sends under now.
func (c *Client) Rules() Rules {
	return c.current.Load().rules
}

// SetRules changes the rules. Sends from now on use a new set of
// connections, so that none made under the old rules is used again.
func (c *Client) SetRules(rules Rules) {
	old := c.current.Swap(c.newState(rules))
	old.transport.CloseIdleConnections()
}

// Close lets the client's idle connections go.
func (c *Client) Close() {
	c.current.Load().transport.CloseIdleConnections()
}

type schemeKey struct{}

type dialPermit struct {
	listed     bool
	loopbackOK bool
}

type dialPermitKey struct{}

// errAddressRefused is a resolved address the rules do not allow.
var errAddressRefused = errors.New("the address rules refuse this address")

func (c *Client) newState(rules Rules) *clientState {
	dialer := &net.Dialer{
		Timeout:   c.options.DialTimeout,
		KeepAlive: 30 * time.Second,
		Resolver:  c.options.resolver,
		ControlContext: func(ctx context.Context, network, address string, _ syscall.RawConn) error {
			permit, ok := ctx.Value(dialPermitKey{}).(dialPermit)
			if !ok {
				return errAddressRefused
			}

			addrPort, err := netip.ParseAddrPort(address)
			if err != nil || !addressAllowed(addrPort.Addr(), permit.listed, permit.loopbackOK) {
				return errAddressRefused
			}

			return nil
		},
	}

	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" && network != "tcp4" && network != "tcp6" {
				return nil, errAddressRefused
			}

			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errAddressRefused
			}

			scheme, _ := ctx.Value(schemeKey{}).(string)
			entry, listed := rules.Listed.match(scheme, host, port)
			permit := dialPermit{listed: listed, loopbackOK: listed && entry.loopback}

			return dialer.DialContext(context.WithValue(ctx, dialPermitKey{}, permit), network, address)
		},
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: c.options.RootCAs},
		TLSHandshakeTimeout:    c.options.TLSTimeout,
		DisableCompression:     true,
		MaxIdleConns:           32,
		MaxIdleConnsPerHost:    maxIdlePerHost,
		MaxConnsPerHost:        maxConnsPerHost,
		IdleConnTimeout:        idleTimeout,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
	}

	return &clientState{
		rules:     rules,
		transport: transport,
		http: &http.Client{
			Transport: transport,
			Timeout:   c.options.RequestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Send posts one encrypted message (at most MaxBody bytes) to endpoint,
// exactly as given, with the Authorization header given (webpush's VAPID
// header), and says what became of it. It checks the address again under
// the rules as they are now, and waits at most the request timeout or
// until ctx is done.
func (c *Client) Send(ctx context.Context, endpoint, authorization string, body []byte) Outcome {
	state := c.current.Load()

	target, err := ValidateEndpoint(endpoint, state.rules)
	if err != nil {
		return Outcome{Result: Permanent, Kind: KindAddress, Host: HostOf(endpoint), Reason: ReasonRules}
	}

	if len(body) == 0 || len(body) > MaxBody {
		return Outcome{Result: Permanent, Kind: KindAnswer, Host: target.Host, Reason: ReasonBody}
	}

	request, err := http.NewRequestWithContext(context.WithValue(ctx, schemeKey{}, target.Scheme), http.MethodPost, target.URL, bytes.NewReader(body))
	if err != nil {
		return Outcome{Result: Permanent, Kind: KindAddress, Host: target.Host, Reason: ReasonRules}
	}

	request.Header.Set("Content-Encoding", "aes128gcm")
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("TTL", TTL)
	request.Header.Set("Urgency", "high")
	request.Header.Set("User-Agent", UserAgent)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}

	response, err := state.http.Do(request)
	if err != nil {
		outcome := classifyError(ctx, err)
		outcome.Host = target.Host

		// A name whose address the rules refuse looks to the person who
		// registered it like a name that does not resolve, so that the
		// outcome tells nobody what this server's resolver answers. The
		// reason still says why, for the admin's log.
		if outcome.Kind == KindAddress && !target.literal {
			outcome.Result, outcome.Kind = Retry, KindUnreachable
		}

		return outcome
	}

	// An answer below 200 is a switch of protocols (Go handles the other
	// 1xx itself): its body is the connection, which the push server could
	// hold open for good. It is no answer to a push.
	if response.StatusCode < 200 {
		_ = response.Body.Close()

		return Outcome{Result: Permanent, Status: response.StatusCode, Kind: KindAnswer, Host: target.Host, Reason: ReasonStatus}
	}

	// The status is what counts; the body is thrown away, and has its own
	// short bound so that a push server cannot hold a send by dribbling it.
	cut := time.AfterFunc(answerTimeout, func() { _ = response.Body.Close() })
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxAnswerBytes))
	cut.Stop()
	_ = response.Body.Close()

	outcome := classifyStatus(response.StatusCode, response.Header.Get("Retry-After"), c.options.Now())
	outcome.Host = target.Host

	return outcome
}

// classifyStatus is what a push server's answer means (RFC 8030 and what
// push servers in use answer: ntfy says 200, and 507 for a phone not yet
// subscribed).
func classifyStatus(status int, retryAfter string, now time.Time) Outcome {
	outcome := Outcome{Status: status, Reason: ReasonStatus}

	switch {
	case status >= 200 && status <= 299:
		outcome.Result = Delivered
	case status == http.StatusNotFound || status == http.StatusGone:
		outcome.Result, outcome.Kind = Gone, KindAnswer
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		outcome.Result, outcome.Kind = Refused, KindAnswer
	case status == http.StatusTooManyRequests || (status >= 500 && status <= 599):
		outcome.Result, outcome.Kind = Retry, KindAnswer
		outcome.RetryAfter = parseRetryAfter(retryAfter, now)
	default:
		outcome.Result, outcome.Kind = Permanent, KindAnswer
	}

	return outcome
}

// parseRetryAfter reads delay-seconds or an HTTP-date, at most
// MaxRetryAfter; 0 for none, one in the past, or one that is not either.
func parseRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}

	var wait time.Duration

	if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
		wait = time.Duration(seconds) * time.Second
	} else if when, err := http.ParseTime(value); err == nil {
		wait = when.Sub(now)
	}

	return min(max(wait, 0), MaxRetryAfter)
}

// classifyError is what a request that got no answer means. The error
// itself goes no further: it spells out the address.
func classifyError(ctx context.Context, err error) Outcome {
	var verification *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var dnsError *net.DNSError
	var netError net.Error

	switch {
	case errors.Is(err, errAddressRefused):
		return Outcome{Result: Permanent, Kind: KindAddress, Reason: ReasonRules}
	case errors.As(err, &verification), errors.As(err, &unknownAuthority), errors.As(err, &hostname), errors.As(err, &invalid):
		return Outcome{Result: Permanent, Kind: KindCertificate, Reason: ReasonCertificate}
	case ctx.Err() != nil && errors.Is(err, context.Canceled):
		return Outcome{Result: Retry, Kind: KindUnreachable, Reason: ReasonCanceled}
	case errors.As(err, &netError) && netError.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return Outcome{Result: Retry, Kind: KindUnreachable, Reason: ReasonTimeout}
	case errors.As(err, &dnsError):
		return Outcome{Result: Retry, Kind: KindUnreachable, Reason: ReasonName}
	}

	return Outcome{Result: Retry, Kind: KindUnreachable, Reason: ReasonConnection}
}
