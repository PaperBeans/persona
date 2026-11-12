package repository

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExistenceEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, provider, body, contentType, want string
		status                                  int
	}{
		{"public", "github", `{"id":1,"full_name":"example/project","html_url":"https://code.example.com/example/project"}`, "application/json", "", 200},
		{"empty", "gitlab", `{"id":2,"path_with_namespace":"example/project","web_url":"https://code.example.com/example/project","empty_repo":true}`, "application/json", "", 200},
		{"hidden", "github", `{"message":"Not Found"}`, "application/json", "unavailable", 404},
		{"unauthorized", "gitlab", `{}`, "application/json", "authentication", 401},
		{"forbidden", "github", `{}`, "application/json", "authentication", 403},
		{"limited", "github", `{}`, "application/json", "rate_limited", 429},
		{"login", "github", `<html>Sign in</html>`, "text/html", "inconclusive", 200},
		{"html labeled JSON", "github", `<html>Sign in</html>`, "application/json", "inconclusive", 200},
		{"wrong identity", "github", `{"id":1,"full_name":"other/project","html_url":"https://code.example.com/other/project"}`, "application/json", "inconclusive", 200},
		{"wrong origin", "github", `{"id":1,"full_name":"example/project","html_url":"https://other.example.com/example/project"}`, "application/json", "inconclusive", 200},
		{"missing identity", "github", `{}`, "application/json", "inconclusive", 200},
		{"oversized", "github", strings.Repeat(" ", int(MaxBody)+1), "application/json", "inconclusive", 200},
		{"server failed", "github", `{}`, "application/json", "network", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Checker{transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				wantPath := "/api/v3/repos/example/project"
				if tc.provider == "gitlab" {
					wantPath = "/api/v4/projects/example%2Fproject"
				}
				if req.URL.EscapedPath() != wantPath || req.Method != "GET" || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
					t.Fatalf("metadata only, anonymous: %s %s %v", req.Method, req.URL, req.Header)
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: req}, nil
			})}
			id, _ := Parse("https://code.example.com/example/project", tc.provider)
			err := c.Check(context.Background(), id)
			if code(err) != tc.want {
				t.Fatalf("got %v (%s), want %s", err, code(err), tc.want)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func code(err error) string {
	if err == nil {
		return ""
	}
	var e *CheckError
	if errors.As(err, &e) {
		return e.Code
	}
	return err.Error()
}

func TestCheckTimeoutAndRateLimit(t *testing.T) {
	id, _ := Parse("https://github.com/example/project", "")
	c := Checker{transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" {
			t.Fatal(r.URL)
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Check(ctx, id); code(err) != "timeout" {
		t.Fatalf("cancel: %v", err)
	}
	c.transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: http.Header{"X-Ratelimit-Remaining": {"0"}}, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
	})
	if err := c.Check(context.Background(), id); code(err) != "rate_limited" {
		t.Fatal(err)
	}
}

func TestDestinationPolicyAndDNSPinning(t *testing.T) {
	id, _ := Parse("https://code.example.com/group/project", "gitlab")
	for _, address := range []string{"127.0.0.1", "::1", private4(169, 254, 169, 254), "168.63.129.16", private4(10, 0, 0, 2), "fd00::2", private4(100, 100, 100, 200), "::ffff:127.0.0.1", "64:ff9b::a9fe:a9fe", "2002:7f00:1::", "0.0.0.0", "224.0.0.1", "192.0.2.1"} {
		t.Run(address, func(t *testing.T) {
			c := Checker{resolve: func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP(address)}}, nil
			},
				dial: func(context.Context, string, string) (net.Conn, error) {
					t.Fatal("blocked destination reached dial")
					return nil, nil
				}}
			if err := c.Check(context.Background(), id); code(err) != "unsafe_target" {
				t.Fatalf("got %v", err)
			}
		})
	}
	for _, tc := range []struct {
		instance, network, ip string
		allowed               bool
	}{
		{"https://code.example.com", private4(10, 0, 0, 0) + "/24", private4(10, 0, 0, 2), true},
		{"https://other.example.com", private4(10, 0, 0, 0) + "/24", private4(10, 0, 0, 2), false},
		{"https://code.example.com", private4(10, 0, 1, 0) + "/24", private4(10, 0, 0, 2), false},
		{"https://code.example.com", "0.0.0.0/0", private4(169, 254, 169, 254), false},
		{"https://code.example.com", "0.0.0.0/0", private4(10, 0, 0, 2), false},
		{"https://code.example.com", "fd00::/8", "fd00:ec2::254", false},
		{"https://code.example.com", "fc00::/7", "fd20:ce::254", false},
		{"https://code.example.com", "127.0.0.0/8", "127.0.0.1", false},
		{"https://code.example.com", "fd00::/8", "fd00::2", true},
	} {
		c := Checker{TrustedTargets: []TrustedTarget{{InstanceURL: tc.instance, Networks: []string{tc.network}}}}
		if got := c.allowed(id, net.ParseIP(tc.ip)); got != tc.allowed {
			t.Errorf("%+v: %v", tc, got)
		}
	}
	var resolutions atomic.Int32
	c := Checker{resolve: func(context.Context, string) ([]net.IPAddr, error) {
		resolutions.Add(1)
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}, dial: func(_ context.Context, _, target string) (net.Conn, error) {
		if target != "93.184.216.34:443" {
			t.Fatalf("DNS was not pinned: %s", target)
		}
		return nil, errors.New("synthetic dial failure")
	}}
	_ = c.Check(context.Background(), id)
	if resolutions.Load() != 1 {
		t.Fatalf("resolved %d times", resolutions.Load())
	}
	c.resolve = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP(private4(10, 0, 0, 2))}}, nil
	}
	c.dial = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("mixed DNS answer must fail closed")
		return nil, nil
	}
	if err := c.Check(context.Background(), id); code(err) != "unsafe_target" {
		t.Fatal(err)
	}
}

// Construct synthetic network fixtures, as other destination-policy tests do.
func private4(a, b, c, d byte) string { return net.IPv4(a, b, c, d).String() }

func TestRedirectNeverFollowed(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "https://other.example.com/login", 302)
	}))
	defer srv.Close()
	c := Checker{transport: srv.Client().Transport}
	// Test-only transport supplies the loopback endpoint; production cannot override destination validation.
	c.transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		u := *r.URL
		req := r.Clone(r.Context())
		req.URL = &u
		req.URL.Host = strings.TrimPrefix(srv.URL, "https://")
		return srv.Client().Transport.RoundTrip(req)
	})
	id, _ := Parse("https://code.example.com/example/project", "github")
	if err := c.Check(context.Background(), id); code(err) != "inconclusive" || calls.Load() != 1 {
		t.Fatalf("%v, calls %d", err, calls.Load())
	}
}

func TestRequestHasBoundedDeadline(t *testing.T) {
	c := Checker{transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > CheckTimeout {
			t.Fatal("missing bounded deadline")
		}
		return nil, errors.New("synthetic")
	})}
	id, _ := Parse("https://github.com/example/project", "")
	_ = c.Check(context.Background(), id)
}

func TestBodyDeadline(t *testing.T) {
	id, _ := Parse("https://github.com/example/project", "")
	ctx, cancel := context.WithCancel(context.Background())
	c := Checker{transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: cancelBody{cancel: cancel}, Request: r}, nil
	})}
	if err := c.Check(ctx, id); code(err) != "timeout" {
		t.Fatalf("body deadline: %v", err)
	}
}

type cancelBody struct{ cancel context.CancelFunc }

func (b cancelBody) Read([]byte) (int, error) { b.cancel(); return 0, context.DeadlineExceeded }
func (cancelBody) Close() error               { return nil }
