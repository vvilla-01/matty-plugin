package pushsend

import (
	"context"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
)

const testAuthorization = "vapid t=a.b.c, k=KEY"

var testBody = []byte("an encrypted message, as far as this package can tell")

// requireClean fails when an outcome repeats the address's secret part.
func requireClean(t *testing.T, outcome Outcome) {
	t.Helper()

	for _, printed := range []string{fmt.Sprintf("%+v", outcome), fmt.Sprintf("%#v", outcome), outcome.String()} {
		require.NotContains(t, printed, secret)
	}
}

// hostPort is a test server's host:port.
func hostPort(t *testing.T, server *httptest.Server) string {
	t.Helper()

	u, err := url.Parse(server.URL)
	require.NoError(t, err)

	return u.Host
}

// seen records what a push server was sent.
type seen struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
}

func (s *seen) handler(answer func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		s.mu.Lock()
		s.requests = append(s.requests, r)
		s.bodies = append(s.bodies, body)
		s.mu.Unlock()

		answer(w, r)
	}
}

func (s *seen) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.requests)
}

func answerStatus(status int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }
}

func TestAPushGoesAsPushServersExpect(t *testing.T) {
	var got seen
	server := httptest.NewServer(got.handler(answerStatus(http.StatusCreated)))
	defer server.Close()

	client := NewClient(Options{Rules: rules(t, hostPort(t, server), false)})
	defer client.Close()

	endpoint := server.URL + "/push/" + secret + "?up=1&x=%2F"
	outcome := client.Send(context.Background(), endpoint, testAuthorization, testBody)
	requireClean(t, outcome)
	require.Equal(t, Delivered, outcome.Result, outcome.String())
	require.Equal(t, http.StatusCreated, outcome.Status)
	require.Equal(t, KindNone, outcome.Kind)
	require.Equal(t, hostPort(t, server), outcome.Host)

	require.Equal(t, 1, got.count())
	r := got.requests[0]
	require.Equal(t, http.MethodPost, r.Method)
	require.Equal(t, "/push/"+secret+"?up=1&x=%2F", r.RequestURI, "the address exactly as given")
	require.Equal(t, testBody, got.bodies[0])
	require.Equal(t, http.Header{
		"Content-Encoding": {"aes128gcm"},
		"Content-Type":     {"application/octet-stream"},
		"Content-Length":   {strconv.Itoa(len(testBody))},
		"Ttl":              {"86400"},
		"Urgency":          {"high"},
		"User-Agent":       {"MattyPlugin"},
		"Authorization":    {testAuthorization},
	}, r.Header, "no Topic, no Accept-Encoding, nothing else")
}

func TestEachAnswerIsClassified(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		status, _ := strconv.Atoi(parts[len(parts)-1])
		if after := r.URL.Query().Get("after"); after != "" {
			w.Header().Set("Retry-After", after)
		}
		if status >= 300 && status < 400 {
			w.Header().Set("Location", "http://"+r.Host+"/elsewhere")
		}
		w.WriteHeader(status)
	}))
	defer server.Close()

	client := NewClient(Options{Rules: rules(t, hostPort(t, server), false), Now: func() time.Time { return now }})
	defer client.Close()

	for _, c := range []struct {
		status int
		after  string
		result Result
		kind   Kind
		wait   time.Duration
	}{
		{200, "", Delivered, KindNone, 0},
		{201, "", Delivered, KindNone, 0},
		{202, "", Delivered, KindNone, 0},
		{204, "", Delivered, KindNone, 0},
		{404, "", Gone, KindAnswer, 0},
		{410, "", Gone, KindAnswer, 0},
		{401, "", Refused, KindAnswer, 0},
		{403, "", Refused, KindAnswer, 0},
		{429, "", Retry, KindAnswer, 0},
		{429, "7", Retry, KindAnswer, 7 * time.Second},
		{429, "3600", Retry, KindAnswer, MaxRetryAfter},
		{503, now.Add(20 * time.Second).Format(http.TimeFormat), Retry, KindAnswer, 20 * time.Second},
		{503, now.Add(-time.Minute).Format(http.TimeFormat), Retry, KindAnswer, 0},
		{503, "soon", Retry, KindAnswer, 0},
		{500, "", Retry, KindAnswer, 0},
		{502, "", Retry, KindAnswer, 0},
		{507, "", Retry, KindAnswer, 0},
		{400, "", Permanent, KindAnswer, 0},
		{405, "", Permanent, KindAnswer, 0},
		{408, "", Permanent, KindAnswer, 0},
		{413, "", Permanent, KindAnswer, 0},
		{200 + 99, "", Delivered, KindNone, 0},
		{301, "", Permanent, KindAnswer, 0},
		{302, "", Permanent, KindAnswer, 0},
		{307, "", Permanent, KindAnswer, 0},
		{308, "", Permanent, KindAnswer, 0},
		{200 + 400, "", Permanent, KindAnswer, 0},
	} {
		endpoint := server.URL + "/" + secret + "/" + strconv.Itoa(c.status)
		if c.after != "" {
			endpoint += "?after=" + url.QueryEscape(c.after)
		}

		outcome := client.Send(context.Background(), endpoint, testAuthorization, testBody)
		requireClean(t, outcome)
		require.Equal(t, c.result, outcome.Result, "%d %s", c.status, outcome)
		require.Equal(t, c.status, outcome.Status)
		require.Equal(t, c.kind, outcome.Kind, "%d", c.status)
		require.Equal(t, c.wait, outcome.RetryAfter, "%d %q", c.status, c.after)
	}
}

func TestARedirectIsNotFollowed(t *testing.T) {
	var elsewhere seen
	target := httptest.NewServer(elsewhere.handler(answerStatus(http.StatusCreated)))
	defer target.Close()

	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/push/"+secret, http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()

	client := NewClient(Options{Rules: rules(t, hostPort(t, redirecting)+"\n"+hostPort(t, target), false)})
	defer client.Close()

	outcome := client.Send(context.Background(), redirecting.URL+"/"+secret, testAuthorization, testBody)
	requireClean(t, outcome)
	require.Equal(t, Permanent, outcome.Result)
	require.Equal(t, http.StatusTemporaryRedirect, outcome.Status)
	require.Equal(t, KindAnswer, outcome.Kind)
	require.Equal(t, 0, elsewhere.count())
}

func TestAnOversizedAnswerIsNotWaitedFor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		chunk := []byte(strings.Repeat("x", 4096))
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	client := NewClient(Options{Rules: rules(t, hostPort(t, server), false), RequestTimeout: 3 * time.Second})
	defer client.Close()

	start := time.Now()
	outcome := client.Send(context.Background(), server.URL+"/"+secret, testAuthorization, testBody)
	require.Equal(t, Delivered, outcome.Result, outcome.String())
	require.Less(t, time.Since(start), 2*time.Second, "an endless answer is read to 4 KiB, not to the end")
}

// sendWithin is Send, failing the test when it has not returned within
// limit rather than waiting for it.
func sendWithin(t *testing.T, limit time.Duration, client *Client, endpoint string) (Outcome, time.Duration) {
	t.Helper()

	type result struct {
		outcome Outcome
		took    time.Duration
	}

	done := make(chan result, 1)
	go func() {
		start := time.Now()
		outcome := client.Send(context.Background(), endpoint, testAuthorization, testBody)
		done <- result{outcome, time.Since(start)}
	}()

	select {
	case r := <-done:
		return r.outcome, r.took
	case <-time.After(limit):
		t.Fatalf("Send has not returned after %s", limit)
	}

	return Outcome{}, 0
}

func TestASwitchOfProtocolsIsRefusedAndLetGo(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			letGo := make(chan struct{}, 1)
			var mu sync.Mutex
			var held []net.Conn

			// The push server reads the push, switches protocols, and then
			// holds the connection without a word for as long as it stays
			// open.
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)

				conn, buffered, err := w.(http.Hijacker).Hijack()
				if err != nil {
					return
				}

				mu.Lock()
				held = append(held, conn)
				mu.Unlock()

				_, _ = buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
				_ = buffered.Flush()

				_, _ = io.Copy(io.Discard, conn)
				_ = conn.Close()
				letGo <- struct{}{}
			}))

			var roots *x509.CertPool
			if scheme == "https" {
				server.StartTLS()
				roots = x509.NewCertPool()
				roots.AddCert(server.Certificate())
			} else {
				server.Start()
			}
			t.Cleanup(server.Close)
			t.Cleanup(func() {
				mu.Lock()
				defer mu.Unlock()
				for _, conn := range held {
					_ = conn.Close()
				}
			})

			client := NewClient(Options{Rules: rules(t, hostPort(t, server), false), RootCAs: roots, RequestTimeout: 500 * time.Millisecond})
			defer client.Close()

			outcome, took := sendWithin(t, 3*time.Second, client, scheme+"://"+hostPort(t, server)+"/"+secret)
			requireClean(t, outcome)
			require.Equal(t, Permanent, outcome.Result, outcome.String())
			require.Equal(t, http.StatusSwitchingProtocols, outcome.Status)
			require.Equal(t, KindAnswer, outcome.Kind)
			require.Less(t, took, 500*time.Millisecond, "refused at once, not read")

			select {
			case <-letGo:
			case <-time.After(time.Second):
				t.Fatal("the switched connection was not let go")
			}
		})
	}
}

func TestADribbledAnswerIsCutAfterASecond(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		w.WriteHeader(http.StatusCreated)
		w.(http.Flusher).Flush()

		for r.Context().Err() == nil {
			if _, err := w.Write([]byte("x")); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	}))
	defer server.Close()

	// The whole request may take its 10 seconds; the body, once the status
	// has come, gets one of them.
	client := NewClient(Options{Rules: rules(t, hostPort(t, server), false)})
	defer client.Close()

	outcome, took := sendWithin(t, 5*time.Second, client, server.URL+"/"+secret)
	requireClean(t, outcome)
	require.Equal(t, Delivered, outcome.Result, outcome.String())
	require.Equal(t, http.StatusCreated, outcome.Status)
	require.Less(t, took, 2*time.Second)
	require.GreaterOrEqual(t, took, 900*time.Millisecond, "the body is read for its second")
}

func TestOversizedHeadersAreNoAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Big", strings.Repeat("x", 20<<10))
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := NewClient(Options{Rules: rules(t, hostPort(t, server), false)})
	defer client.Close()

	outcome := client.Send(context.Background(), server.URL+"/"+secret, testAuthorization, testBody)
	requireClean(t, outcome)
	require.Equal(t, Retry, outcome.Result, outcome.String())
	require.Equal(t, 0, outcome.Status)
	require.Equal(t, KindUnreachable, outcome.Kind)
}

// tarpit accepts connections and never says a word.
func tarpit(t *testing.T) (address string, accepted *atomic.Int32) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	accepted = &atomic.Int32{}
	var conns []net.Conn
	var mu sync.Mutex

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			accepted.Add(1)
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()

	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
	})

	return listener.Addr().String(), accepted
}

func TestATarpitTimesOut(t *testing.T) {
	address, accepted := tarpit(t)

	client := NewClient(Options{
		Rules:          rules(t, address, false),
		TLSTimeout:     300 * time.Millisecond,
		RequestTimeout: 500 * time.Millisecond,
	})
	defer client.Close()

	for _, scheme := range []string{"http", "https"} {
		start := time.Now()
		outcome := client.Send(context.Background(), scheme+"://"+address+"/"+secret, testAuthorization, testBody)
		requireClean(t, outcome)
		require.Equal(t, Retry, outcome.Result, scheme+" "+outcome.String())
		require.Equal(t, KindUnreachable, outcome.Kind)
		require.Equal(t, ReasonTimeout, outcome.Reason, scheme)
		require.Equal(t, 0, outcome.Status)
		require.Less(t, time.Since(start), 2*time.Second, scheme)
	}

	require.Positive(t, accepted.Load())
}

func TestACallersDeadlineEndsASend(t *testing.T) {
	address, _ := tarpit(t)

	client := NewClient(Options{Rules: rules(t, address, false)})
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)

	start := time.Now()
	outcome := client.Send(ctx, "http://"+address+"/"+secret, testAuthorization, testBody)
	require.Equal(t, Retry, outcome.Result)
	require.Equal(t, ReasonCanceled, outcome.Reason)
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestACertificateThatDoesNotVerifyIsPermanent(t *testing.T) {
	var got seen
	server := httptest.NewTLSServer(got.handler(answerStatus(http.StatusCreated)))
	defer server.Close()

	client := NewClient(Options{Rules: rules(t, hostPort(t, server), false)})
	defer client.Close()

	outcome := client.Send(context.Background(), server.URL+"/"+secret, testAuthorization, testBody)
	requireClean(t, outcome)
	require.Equal(t, Permanent, outcome.Result, outcome.String())
	require.Equal(t, KindCertificate, outcome.Kind)
	require.Equal(t, 0, got.count())

	// Trusted, the same server takes it.
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	trusting := NewClient(Options{Rules: rules(t, hostPort(t, server), false), RootCAs: roots})
	defer trusting.Close()

	outcome = trusting.Send(context.Background(), server.URL+"/"+secret, testAuthorization, testBody)
	require.Equal(t, Delivered, outcome.Result, outcome.String())
	require.Equal(t, 1, got.count())
}

// refusedServer is a TLS server the client trusts, which counts every
// connection made to it, so that a test can see that a refused address was
// never even connected to.
func refusedServer(t *testing.T) (port string, roots *x509.CertPool, connections *atomic.Int32) {
	t.Helper()

	connections = &atomic.Int32{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(answerStatus(http.StatusCreated)))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	t.Cleanup(server.Close)

	roots = x509.NewCertPool()
	roots.AddCert(server.Certificate())

	_, port, err := net.SplitHostPort(hostPort(t, server))
	require.NoError(t, err)

	return port, roots, connections
}

func TestANameResolvingToThisMachineIsRefusedAtTheDial(t *testing.T) {
	port, roots, connections := refusedServer(t)

	client := NewClient(Options{RootCAs: roots})
	defer client.Close()

	// By name, the refusal looks as a name that does not resolve does; only
	// the reason says why.
	for _, name := range []string{"localhost", "push.localhost"} {
		outcome := client.Send(context.Background(), "https://"+name+":"+port+"/"+secret, testAuthorization, testBody)
		requireRefusedByName(t, outcome)
		require.Equal(t, name+":"+port, outcome.Host)
	}

	require.Zero(t, connections.Load())
}

// requireRefusedByName holds the outcome of a push whose host, given by
// name, led only to addresses the rules refuse: what a failed lookup gives,
// with the reason kept.
func requireRefusedByName(t *testing.T, outcome Outcome) {
	t.Helper()

	requireClean(t, outcome)
	require.Equal(t, Retry, outcome.Result, outcome.String())
	require.Equal(t, KindUnreachable, outcome.Kind, outcome.String())
	require.Equal(t, ReasonRules, outcome.Reason, outcome.String())
	require.Zero(t, outcome.Status)
}

func TestARefusedNameAndANameThatDoesNotResolveLookAlike(t *testing.T) {
	port, roots, connections := refusedServer(t)

	refused := NewClient(Options{RootCAs: roots, resolver: fakeDNS(t, answers("10.0.0.7"))})
	defer refused.Close()
	nowhere := NewClient(Options{RootCAs: roots, resolver: fakeDNS(t, answers())})
	defer nowhere.Close()

	endpoint := "https://grafana.corp.example:" + port + "/" + secret
	a := refused.Send(context.Background(), endpoint, testAuthorization, testBody)
	b := nowhere.Send(context.Background(), endpoint, testAuthorization, testBody)
	requireRefusedByName(t, a)
	requireClean(t, b)
	require.Equal(t, ReasonName, b.Reason, b.String())
	require.Equal(t, b.Result, a.Result)
	require.Equal(t, b.Kind, a.Kind)
	require.Equal(t, b.Host, a.Host)

	// Written as an IP address, the refusal is the address's.
	literal := refused.Send(context.Background(), "https://10.0.0.7:"+port+"/"+secret, testAuthorization, testBody)
	require.Equal(t, Permanent, literal.Result)
	require.Equal(t, KindAddress, literal.Kind)

	require.Zero(t, connections.Load())
}

// answers is a fixed answer for fakeDNS.
func answers(texts ...string) func() []netip.Addr {
	addrs := make([]netip.Addr, 0, len(texts))
	for _, text := range texts {
		addrs = append(addrs, netip.MustParseAddr(text))
	}

	return func() []netip.Addr { return addrs }
}

// fakeDNS is a resolver that answers every name with the addresses answer
// gives at the time of the lookup, in that order (A records for the IPv4
// ones, AAAA for the IPv6 ones), as a hostile name server would.
func fakeDNS(t *testing.T, answer func() []netip.Addr) *net.Resolver {
	t.Helper()

	return &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()

			go func() {
				defer server.Close()

				for {
					var length [2]byte
					if _, err := io.ReadFull(server, length[:]); err != nil {
						return
					}

					query := make([]byte, binary.BigEndian.Uint16(length[:]))
					if _, err := io.ReadFull(server, query); err != nil {
						return
					}

					var parser dnsmessage.Parser
					header, err := parser.Start(query)
					if err != nil {
						return
					}
					question, err := parser.Question()
					if err != nil {
						return
					}

					builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: header.ID, Response: true, Authoritative: true})
					_ = builder.StartQuestions()
					_ = builder.Question(question)
					_ = builder.StartAnswers()
					record := dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 60}
					for _, addr := range answer() {
						switch {
						case question.Type == dnsmessage.TypeA && addr.Is4():
							_ = builder.AResource(record, dnsmessage.AResource{A: addr.As4()})
						case question.Type == dnsmessage.TypeAAAA && addr.Is6():
							_ = builder.AAAAResource(record, dnsmessage.AAAAResource{AAAA: addr.As16()})
						}
					}
					reply, err := builder.Finish()
					if err != nil {
						return
					}

					binary.BigEndian.PutUint16(length[:], uint16(len(reply)))
					if _, err := server.Write(append(length[:], reply...)); err != nil {
						return
					}
				}
			}()

			return client, nil
		},
	}
}

func TestANameThatResolvesToALoopbackAddressIsRefusedEvenListed(t *testing.T) {
	port, roots, connections := refusedServer(t)

	// push.example answers 127.0.0.1. Not listed: refused. Listed, by a name
	// that is not this machine's: still refused, since loopback is only for
	// an entry that names this machine itself.
	for _, list := range []string{"", "push.example:" + port, "push.example"} {
		options := Options{Rules: rules(t, list, false), RootCAs: roots, resolver: fakeDNS(t, answers("127.0.0.1"))}
		client := NewClient(options)

		outcome := client.Send(context.Background(), "https://push.example:"+port+"/"+secret, testAuthorization, testBody)
		requireRefusedByName(t, outcome)
		client.Close()
	}

	// The same name answering an address no push server may be at unlisted
	// (documentation space): refused unlisted; listed, the dial is tried
	// (and finds nothing there), so the rules let it through.
	for list, refusedByRules := range map[string]bool{"": true, "push.example:" + port: false} {
		options := Options{Rules: rules(t, list, false), RootCAs: roots, resolver: fakeDNS(t, answers("192.0.2.1")), DialTimeout: 200 * time.Millisecond, RequestTimeout: time.Second}
		client := NewClient(options)

		outcome := client.Send(context.Background(), "https://push.example:"+port+"/"+secret, testAuthorization, testBody)
		requireClean(t, outcome)
		require.Equal(t, KindUnreachable, outcome.Kind, "%q %s", list, outcome)
		require.Equal(t, refusedByRules, outcome.Reason == ReasonRules, "%q %s", list, outcome)
		require.NotEqual(t, ReasonName, outcome.Reason, "the fake name server answered")
		client.Close()
	}

	require.Zero(t, connections.Load())
}

// countingServer is a plain HTTP server on the loopback address given,
// which counts the connections made to it; each answer closes its
// connection, so that every push needs a new one.
func countingServer(t *testing.T, network, address string) (port string, connections *atomic.Int32) {
	t.Helper()

	listener, err := net.Listen(network, address)
	if err != nil {
		t.Skipf("no %s loopback here: %v", network, err)
	}

	connections = &atomic.Int32{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusCreated)
	}))
	_ = server.Listener.Close()
	server.Listener = listener
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)

	_, port, err = net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)

	return port, connections
}

func TestANameAnsweringAPrivateIPv6AddressIsRefused(t *testing.T) {
	port, connections := countingServer(t, "tcp6", "[::1]:0")

	// Unlisted: neither this machine nor a unique local address. Listed by
	// a name that is not this machine's: still not this machine.
	for _, c := range []struct {
		list   string
		answer []string
	}{
		{"", []string{"::1"}},
		{"", []string{"fd00::1"}},
		{"", []string{"fd00:ec2::254"}},
		{"push.example:" + port, []string{"::1"}},
	} {
		// https, as an unlisted push server must be; a connection, had one
		// been made, would be counted all the same.
		client := NewClient(Options{Rules: rules(t, c.list, false), resolver: fakeDNS(t, answers(c.answer...))})
		outcome := client.Send(context.Background(), "https://push.example:"+port+"/"+secret, testAuthorization, testBody)
		requireRefusedByName(t, outcome)
		client.Close()
	}

	require.Zero(t, connections.Load(), "nothing reached [::1]")
}

func TestEveryAddressANameAnswersIsChecked(t *testing.T) {
	port, connections := countingServer(t, "tcp4", "127.0.0.1:0")

	// push.localhost is listed and names this machine, so loopback is
	// allowed; 0.0.0.0, which on this machine also reaches the server, is
	// never allowed. 127.0.0.2 has nothing listening, so the dial goes on
	// to the next address.
	list := "push.localhost:" + port
	endpoint := "http://push.localhost:" + port + "/" + secret

	// The dial does go on past an address that fails: the server is reached
	// at the second.
	client := NewClient(Options{Rules: rules(t, list, false), resolver: fakeDNS(t, answers("127.0.0.2", "127.0.0.1"))})
	outcome := client.Send(context.Background(), endpoint, testAuthorization, testBody)
	require.Equal(t, Delivered, outcome.Result, outcome.String())
	client.Close()
	require.Equal(t, int32(1), connections.Load())

	// An allowed address first and a refused one after, or the other way
	// round: the refused one is never connected to.
	for _, answer := range [][]string{{"127.0.0.2", "0.0.0.0"}, {"0.0.0.0", "127.0.0.2"}} {
		client := NewClient(Options{Rules: rules(t, list, false), resolver: fakeDNS(t, answers(answer...))})
		outcome := client.Send(context.Background(), endpoint, testAuthorization, testBody)
		requireClean(t, outcome)
		require.Equal(t, Retry, outcome.Result, "%v %s", answer, outcome)
		require.Equal(t, KindUnreachable, outcome.Kind, "%v %s", answer, outcome)
		client.Close()
	}

	require.Equal(t, int32(1), connections.Load(), "0.0.0.0 was never connected to")
}

func TestANewConnectionChecksTheNamesAnswerAgain(t *testing.T) {
	port, connections := countingServer(t, "tcp4", "127.0.0.1:0")

	var current atomic.Pointer[[]netip.Addr]
	set := func(texts ...string) {
		addrs := answers(texts...)()
		current.Store(&addrs)
	}
	set("127.0.0.1")

	client := NewClient(Options{
		Rules:    rules(t, "push.localhost:"+port, false),
		resolver: fakeDNS(t, func() []netip.Addr { return *current.Load() }),
	})
	defer client.Close()

	endpoint := "http://push.localhost:" + port + "/" + secret
	require.Equal(t, Delivered, client.Send(context.Background(), endpoint, testAuthorization, testBody).Result)
	require.Equal(t, int32(1), connections.Load())

	// The name now answers an address that is never allowed, which on this
	// machine reaches the same server: the next connection is refused.
	set("0.0.0.0")
	requireRefusedByName(t, client.Send(context.Background(), endpoint, testAuthorization, testBody))
	require.Equal(t, int32(1), connections.Load())

	// And back: the next connection is made.
	set("127.0.0.1")
	require.Equal(t, Delivered, client.Send(context.Background(), endpoint, testAuthorization, testBody).Result)
	require.Equal(t, int32(2), connections.Load())
}

func TestAListedHostWithAndWithoutAPort(t *testing.T) {
	var got seen
	server := httptest.NewServer(got.handler(answerStatus(http.StatusCreated)))
	defer server.Close()

	host, port, err := net.SplitHostPort(hostPort(t, server))
	require.NoError(t, err)

	// Listed with its port: delivered over http.
	client := NewClient(Options{Rules: rules(t, host+":"+port, false)})
	outcome := client.Send(context.Background(), "http://"+host+":"+port+"/"+secret, testAuthorization, testBody)
	require.Equal(t, Delivered, outcome.Result, outcome.String())
	client.Close()

	// Listed without a port: that lists only port 80, so this one is not
	// listed, and neither http nor this machine's address is allowed.
	client = NewClient(Options{Rules: rules(t, host, false)})
	for _, scheme := range []string{"http", "https"} {
		outcome = client.Send(context.Background(), scheme+"://"+host+":"+port+"/"+secret, testAuthorization, testBody)
		requireClean(t, outcome)
		require.Equal(t, Permanent, outcome.Result, scheme)
		require.Equal(t, KindAddress, outcome.Kind, scheme)
	}
	client.Close()

	// By name: localhost listed with its port is this machine, and allowed.
	client = NewClient(Options{Rules: rules(t, "localhost:"+port, false)})
	outcome = client.Send(context.Background(), "http://localhost:"+port+"/"+secret, testAuthorization, testBody)
	require.Equal(t, Delivered, outcome.Result, outcome.String())
	client.Close()

	require.Equal(t, 2, got.count())
}

func TestChangedRulesApplyToTheNextSend(t *testing.T) {
	var got seen
	server := httptest.NewServer(got.handler(answerStatus(http.StatusCreated)))
	defer server.Close()

	client := NewClient(Options{Rules: rules(t, hostPort(t, server), false)})
	defer client.Close()

	endpoint := server.URL + "/" + secret
	require.Equal(t, Delivered, client.Send(context.Background(), endpoint, testAuthorization, testBody).Result)

	client.SetRules(Rules{})
	require.Equal(t, 0, client.Rules().Listed.Len())
	outcome := client.Send(context.Background(), endpoint, testAuthorization, testBody)
	require.Equal(t, Permanent, outcome.Result)
	require.Equal(t, KindAddress, outcome.Kind)

	client.SetRules(rules(t, hostPort(t, server), true))
	require.Equal(t, Delivered, client.Send(context.Background(), endpoint, testAuthorization, testBody).Result)
	require.Equal(t, 2, got.count())
}

func TestAMessageOfTheWrongSizeIsNotSent(t *testing.T) {
	var got seen
	server := httptest.NewServer(got.handler(answerStatus(http.StatusCreated)))
	defer server.Close()

	client := NewClient(Options{Rules: rules(t, hostPort(t, server), false)})
	defer client.Close()

	for _, body := range [][]byte{nil, make([]byte, MaxBody+1)} {
		outcome := client.Send(context.Background(), server.URL+"/"+secret, testAuthorization, body)
		require.Equal(t, Permanent, outcome.Result)
		require.Equal(t, ReasonBody, outcome.Reason)
	}

	require.Equal(t, Delivered, client.Send(context.Background(), server.URL+"/"+secret, testAuthorization, make([]byte, MaxBody)).Result)
	require.Equal(t, 1, got.count())
}

func TestTheClientUsesNoProxyAndNoCompression(t *testing.T) {
	client := NewClient(Options{})
	defer client.Close()

	state := client.current.Load()
	require.Nil(t, state.transport.Proxy)
	require.True(t, state.transport.DisableCompression)
	require.Equal(t, int64(16<<10), state.transport.MaxResponseHeaderBytes)
	require.Equal(t, 2, state.transport.MaxIdleConnsPerHost)
	require.Equal(t, 60*time.Second, state.transport.IdleConnTimeout)
	require.Equal(t, 5*time.Second, state.transport.TLSHandshakeTimeout)
	require.Equal(t, 10*time.Second, state.http.Timeout)
	require.NotNil(t, state.http.CheckRedirect)
}

func TestAnAddressThatNoLongerPassesIsNotSent(t *testing.T) {
	client := NewClient(Options{})
	defer client.Close()

	for _, endpoint := range []string{
		"http://ntfy.sh/" + secret,
		"https://127.0.0.1/" + secret,
		"https://[::ffff:127.0.0.1]/" + secret,
		"https://[::7f00:1]/" + secret,
		"https://user@ntfy.sh/" + secret,
		"not an address " + secret,
	} {
		outcome := client.Send(context.Background(), endpoint, testAuthorization, testBody)
		requireClean(t, outcome)
		require.Equal(t, Permanent, outcome.Result, endpoint)
		require.Equal(t, KindAddress, outcome.Kind, endpoint)
	}
}

func TestOutcomeString(t *testing.T) {
	require.Equal(t, "delivered ntfy.sh 201", Outcome{Result: Delivered, Host: "ntfy.sh", Status: 201, Reason: ReasonStatus}.String())
	require.Equal(t, "retry push.lan:8443 unreachable (timeout)", Outcome{Result: Retry, Host: "push.lan:8443", Kind: KindUnreachable, Reason: ReasonTimeout}.String())
	require.Equal(t, "refused ntfy.sh 403 answer", Outcome{Result: Refused, Host: "ntfy.sh", Status: 403, Kind: KindAnswer, Reason: ReasonStatus}.String())
}
