package shareproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"time"

	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/shareproof"
)

// readIngressKey reads only a private owned regular configuration file, without logging its values.
func readIngressKey(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 || !osproc.OwnedByMe(before) {
		return nil, errors.New("share-http needs an owned mode0600 hub configuration")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("sharing ingress configuration unavailable")
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("sharing ingress configuration changed")
	}
	body, err := io.ReadAll(io.LimitReader(f, maxResponse+1))
	var config struct {
		Sharing struct {
			IngressKey string `json:"ingress_key"`
		} `json:"sharing"`
	}
	if err != nil || len(body) > maxResponse || json.Unmarshal(body, &config) != nil {
		return nil, errors.New("invalid sharing ingress configuration")
	}
	key := shareproof.DecodeKey(config.Sharing.IngressKey)
	if len(key) != 32 {
		return nil, errors.New("configure the private sharing ingress key first")
	}
	return key, nil
}

// probeIngress verifies the hub's local proof support before opening the public listener.
func probeIngress(ctx context.Context, c Config) error {
	upstream, origin, err := validate(c)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.String()+shareproof.APIPrefix+"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", nil)
	if err != nil {
		return errors.New("sharing ingress probe unavailable")
	}
	r.Host = origin.Host
	r.Header.Set(shareproof.Header, shareproof.Sign(c.IngressKey, r, netip.MustParseAddr("127.0.0.1"), time.Now()))
	transport := &http.Transport{Proxy: nil, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(r)
	if err != nil {
		return errors.New("sharing ingress authentication probe failed")
	}
	defer res.Body.Close()
	if res.Header.Get(shareproof.Acknowledgement) != shareproof.Accepted || (res.StatusCode != 200 && res.StatusCode != 404 && res.StatusCode != 429) {
		return errors.New("upstream does not authenticate sharing ingress requests")
	}
	return nil
}
