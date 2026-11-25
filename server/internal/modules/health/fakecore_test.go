package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// The module's tests run it on a fake core: the settings rows, the open apps' events and the hub's push, as the
// assistant serves them through internal/healthhost (health_layer.go there). The core's own part of what the module
// meets (the tool's space guard, extension.Fit of the intake page for older apps, the push outbox, the real SQL) is
// tested in internal/assistant (health_module_test.go).

const RoleHeader = assistantwire.RoleHeader

type (
	Notification  = assistantwire.Notification
	NotifyRequest = assistantwire.NotifyRequest
	NotifyResult  = assistantwire.NotifyResult
	// Store is what a test's core keeps: the settings rows, and the intake when it implements healthIntakeStore.
	Store = settingsStore
)

// Settings are the owner's settings the module reads of the core.
type Settings struct {
	Timezone string
}

// fakeHub records the open apps' events and the pushes asked of the hub.
type fakeHub struct {
	mu           sync.Mutex
	events       []string
	pushes       []Notification
	refuseNotify error        // notify.request is the hub's refusal (answered) while set
	notifyResult NotifyResult // the held/reason answer notify.request returns once it succeeds
}

func newFakeHub() *fakeHub { return &fakeHub{} }

func (f *fakeHub) NotifyRequest(_ context.Context, p NotifyRequest) (NotifyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p.Notification.Kind != assistantwire.NotifyHealthWater {
		return NotifyResult{}, fmt.Errorf("kind %q", p.Notification.Kind)
	}
	if f.refuseNotify != nil {
		return NotifyResult{}, f.refuseNotify
	}
	f.pushes = append(f.pushes, p.Notification)
	return f.notifyResult, nil
}

// pushed are the pushes the hub took.
func (f *fakeHub) pushed() []Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.pushes)
}

func (f *fakeHub) event(kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, kind)
}

type notifier interface {
	NotifyRequest(context.Context, NotifyRequest) (NotifyResult, error)
}

// Options make a fake core: its hub (a *fakeHub or one that embeds it), its store and its log.
type Options struct {
	Hub   notifier
	Store func() Store
	Logf  func(format string, args ...any)
}

// fakeCore is the core as the module meets it (Host).
type fakeCore struct {
	o        Options
	mu       sync.Mutex
	settings Settings
	pageBase string
}

func newService(o Options) *Service { return New(&fakeCore{o: o}) }

// newServiceOn is the module on a fake core with hub f and store st.
func newServiceOn(t testing.TB, f *fakeHub, st Store) *Service {
	return newService(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t)})
}

// withSettings is s (the fake core needs nothing more).
func withSettings(_ testing.TB, _ *fakeHub, s *Service) *Service { return s }

// setSettings changes the owner's settings of s's core.
func setSettings(t testing.TB, s *Service, fn func(*Settings)) {
	t.Helper()
	c := s.h.(*fakeCore)
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(&c.settings)
}

func testLogf(t testing.TB) func(string, ...any) {
	return func(format string, args ...any) { t.Logf(format, args...) }
}

// ServeHTTP serves the module's routes as the assistant does.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	s.Routes(func(pattern string, fn http.HandlerFunc) { mux.HandleFunc(pattern, fn) })
	mux.ServeHTTP(w, r)
}

func (c *fakeCore) Logf(format string, args ...any) {
	if c.o.Logf != nil {
		c.o.Logf(format, args...)
	}
}

func (c *fakeCore) store() Store {
	if c.o.Store == nil {
		return nil
	}
	return c.o.Store()
}

func (c *fakeCore) Settings() settingsStore {
	if st := c.store(); st != nil {
		return st
	}
	return nil
}

func (c *fakeCore) IntakeStore() healthIntakeStore {
	st, _ := c.store().(healthIntakeStore)
	return st
}

func (c *fakeCore) ImportStore() healthWaterImportStore {
	st, _ := c.store().(healthWaterImportStore)
	return st
}

func (c *fakeCore) WaterExtensions() waterExtensions {
	e, _ := c.o.Hub.(waterExtensions)
	return e
}

func (c *fakeCore) Event(_ context.Context, ev map[string]any) {
	kind, _ := ev["type"].(string)
	if f, ok := c.o.Hub.(interface{ event(string) }); ok {
		f.event(kind)
	}
}

func (c *fakeCore) Lang(context.Context) i18n.Lang { return i18n.Default }

func (c *fakeCore) Timezone(context.Context) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings.Timezone
}

func (c *fakeCore) Location(ctx context.Context) *time.Location { return zoneOf(c.Timezone(ctx)) }

func (c *fakeCore) Standby(context.Context) bool { return false }

func (c *fakeCore) QuietPush(context.Context) bool { return false }

func (c *fakeCore) Notify(ctx context.Context, n Notification) (NotifyResult, error) {
	return c.o.Hub.NotifyRequest(ctx, NotifyRequest{Notification: n})
}

// FitPage leaves the view as the module drew it: fitting for older apps is the core's (extension.Fit), tested there.
func (c *fakeCore) FitPage(*wire.View, int, i18n.Lang) error { return nil }

func (c *fakeCore) UIVersion() int { return 11 }

// zoneOf is the time zone named tz, else this machine's own, as the assistant's tools read it (zone.go there).
func zoneOf(tz string) *time.Location {
	if tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return time.Local
}

// memStore keeps settings rows in memory, with the failures a test asks for.
type memStore struct {
	mu          sync.Mutex
	vals        map[string][]byte
	puts        int
	failSetting error
	failPut     error
	failKeys    map[string]error
}

// putCount returns the number of settings writes recorded under m.mu.
func (m *memStore) putCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.puts
}

// failKey makes reading key fail with err from now on; nil reads it again.
func (m *memStore) failKey(key string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failKeys == nil {
		m.failKeys = map[string]error{}
	}
	if err == nil {
		delete(m.failKeys, key)
	} else {
		m.failKeys[key] = err
	}
}

// setFailPut makes writing settings fail with err from now on; nil writes them again.
func (m *memStore) setFailPut(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failPut = err
}

func (m *memStore) Setting(_ context.Context, key string, out any) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSetting != nil {
		return false, m.failSetting
	}
	if err := m.failKeys[key]; err != nil {
		return false, err
	}
	b, ok := m.vals[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(b, out)
}

func (m *memStore) PutSetting(_ context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPut != nil {
		return m.failPut
	}
	if m.vals == nil {
		m.vals = map[string][]byte{}
	}
	m.vals[key] = b
	m.puts++
	return nil
}

func (c *fakeCore) StrengthStore() strengthStore {
	st, _ := c.store().(strengthStore)
	return st
}
func (c *fakeCore) CardShown(ctx context.Context, card assistantwire.StrengthCard) {
	c.Event(ctx, map[string]any{"type": "assistant_feed"})
}
func (c *fakeCore) CardChanged(ctx context.Context, card assistantwire.StrengthCard) {
	c.Event(ctx, map[string]any{"type": "assistant_view"})
}
func (c *fakeCore) PageLink(_ context.Context, ui, state string) string {
	if c.pageBase == "" {
		return ""
	}
	return c.pageBase + "/extension/-health-intake/" + ui + "?state=" + url.QueryEscape(state)
}

func (s *Service) intakeTool(ctx context.Context, args json.RawMessage) (string, error) {
	return s.Tool(ctx, assistantwire.HealthToolCall{Name: assistantwire.CapabilityHealthIntake, Args: args})
}
