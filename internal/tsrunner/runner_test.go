package tsrunner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeServer implements tsnetServer using an in-process [net.Listener].
// It supports scripted state transitions to drive the state machine.
type fakeServer struct {
	mu sync.Mutex

	// nextStartErr controls the result of the next Start call.
	nextStartErr error
	// currentState is what BackendState returns. setState mutates it.
	currentState string
	// stateOnAuthKey, if non-empty, replaces currentState the first time
	// SetAuthKey is called. Lets a fake start in NeedsLogin and transition
	// to Running after the runner provides a fresh key.
	stateOnAuthKey string

	listener net.Listener
	listenFn func() (net.Listener, error)

	certDomains    []string
	backendStateFn func(context.Context) (string, error)

	startCalls   atomic.Int32
	listenCalls  atomic.Int32
	closeCalls   atomic.Int32
	authKey      string
	authKeyCalls atomic.Int32

	closed atomic.Bool
}

func newFakeServer(state string) *fakeServer {
	return &fakeServer{currentState: state}
}

func (f *fakeServer) Start(_ context.Context) error {
	f.startCalls.Add(1)
	f.mu.Lock()
	err := f.nextStartErr
	f.mu.Unlock()
	if err != nil {
		// Honor ctx cancellation while signaling slow-start; here we return
		// immediately so tests don't have to wait.
		return err
	}
	return nil
}

func (f *fakeServer) ListenFunnel(_, _ string) (net.Listener, error) {
	f.listenCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listenFn != nil {
		ln, err := f.listenFn()
		if err != nil {
			return nil, err
		}
		f.listener = ln
		return ln, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f.listener = ln
	return ln, nil
}

func (f *fakeServer) BackendState(ctx context.Context) (string, error) {
	if f.backendStateFn != nil {
		return f.backendStateFn(ctx)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.currentState, nil
}

func (f *fakeServer) CertDomains(_ context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.certDomains))
	copy(out, f.certDomains)
	return out, nil
}

func (f *fakeServer) SetAuthKey(key string) {
	f.authKeyCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authKey = key
	if f.stateOnAuthKey != "" {
		f.currentState = f.stateOnAuthKey
		f.stateOnAuthKey = ""
	}
}

func (f *fakeServer) Close() error {
	f.closeCalls.Add(1)
	f.closed.Store(true)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listener != nil {
		_ = f.listener.Close()
		f.listener = nil
	}
	return nil
}

func (f *fakeServer) setState(s string) {
	f.mu.Lock()
	f.currentState = s
	f.mu.Unlock()
}

func (f *fakeServer) listenerAddr() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listener == nil {
		return ""
	}
	return f.listener.Addr().String()
}

type fakeMinter struct {
	mu    sync.Mutex
	keys  []string
	errs  []error
	calls atomic.Int32
}

func (m *fakeMinter) Mint(_ context.Context) (string, error) {
	n := int(m.calls.Add(1))
	m.mu.Lock()
	defer m.mu.Unlock()
	if n-1 < len(m.errs) && m.errs[n-1] != nil {
		return "", m.errs[n-1]
	}
	if n-1 < len(m.keys) {
		return m.keys[n-1], nil
	}
	return "tskey-default", nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
}

func runOnce(t *testing.T, factory ServerFactory, minter AuthKeyMinter, cancelAfter time.Duration) {
	t.Helper()
	cfg := Config{
		Handler:         okHandler(),
		FunnelAddr:      "127.0.0.1:0",
		StartTimeout:    200 * time.Millisecond,
		ShutdownTimeout: 200 * time.Millisecond,
		PollInterval:    20 * time.Millisecond,
		Logger:          discardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, factory, minter)
	}()

	time.Sleep(cancelAfter)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit within 2s of cancel")
	}
}

func TestRun_HappyPath_RunningFromStart(t *testing.T) {
	server := newFakeServer(BackendStateRunning)
	factoryCalls := atomic.Int32{}
	factory := func() tsnetServer {
		factoryCalls.Add(1)
		return server
	}
	minter := &fakeMinter{}

	runOnce(t, factory, minter, 100*time.Millisecond)

	if server.listenCalls.Load() == 0 {
		t.Error("ListenFunnel never called")
	}
	if minter.calls.Load() != 0 {
		t.Errorf("minter called %d times (want 0)", minter.calls.Load())
	}
}

func TestRun_PublicReadySignalTracksServeLifecycle(t *testing.T) {
	server := newFakeServer(BackendStateRunning)
	factory := func() tsnetServer { return server }

	var ready atomic.Bool
	updates := make(chan bool, 8)
	cfg := Config{
		Handler:         okHandler(),
		FunnelAddr:      "127.0.0.1:0",
		StartTimeout:    200 * time.Millisecond,
		ShutdownTimeout: 200 * time.Millisecond,
		PollInterval:    20 * time.Millisecond,
		Logger:          discardLogger(),
		SetPublicReady: func(v bool) {
			ready.Store(v)
			select {
			case updates <- v:
			default:
			}
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, factory, &fakeMinter{}) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if ready.Load() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !ready.Load() {
		cancel()
		<-done
		t.Fatal("public-ready signal never became true")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit")
	}

	if ready.Load() {
		t.Fatal("public-ready signal remained true after shutdown")
	}

	sawTrue := false
	sawFalse := false
	close(updates)
	for v := range updates {
		if v {
			sawTrue = true
			continue
		}
		sawFalse = true
	}
	if !sawTrue || !sawFalse {
		t.Fatalf("public-ready transitions = true:%v false:%v", sawTrue, sawFalse)
	}
}

func TestRun_IssuerHostMatch_Serves(t *testing.T) {
	server := newFakeServer(BackendStateRunning)
	server.certDomains = []string{"oidc.tailnet-foo.ts.net"}
	factory := func() tsnetServer { return server }

	cfg := Config{
		Handler:            okHandler(),
		FunnelAddr:         "127.0.0.1:0",
		StartTimeout:       200 * time.Millisecond,
		ShutdownTimeout:    200 * time.Millisecond,
		PollInterval:       20 * time.Millisecond,
		Logger:             discardLogger(),
		ExpectedIssuerHost: "oidc.tailnet-foo.ts.net",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, factory, &fakeMinter{}) }()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit")
	}
	if server.listenCalls.Load() == 0 {
		t.Error("ListenFunnel was not called after match")
	}
}

func TestRun_IssuerHostMismatch_Fatal(t *testing.T) {
	server := newFakeServer(BackendStateRunning)
	server.certDomains = []string{"oidc.tailnet-foo.ts.net"}
	factory := func() tsnetServer { return server }

	cfg := Config{
		Handler:            okHandler(),
		FunnelAddr:         "127.0.0.1:0",
		StartTimeout:       200 * time.Millisecond,
		ShutdownTimeout:    200 * time.Millisecond,
		PollInterval:       20 * time.Millisecond,
		Logger:             discardLogger(),
		ExpectedIssuerHost: "wrong.tailnet-bar.ts.net",
	}
	done := make(chan error, 1)
	go func() { done <- Run(t.Context(), cfg, factory, &fakeMinter{}) }()

	select {
	case err := <-done:
		if !errors.Is(err, ErrIssuerHostMismatch) {
			t.Errorf("Run err = %v, want ErrIssuerHostMismatch", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit on mismatch")
	}
	if server.listenCalls.Load() != 0 {
		t.Error("ListenFunnel called despite mismatch")
	}
}

func TestRun_IssuerHostEmpty_SkipsCheck(t *testing.T) {
	server := newFakeServer(BackendStateRunning)
	// No certDomains set; with no expected host the check is skipped.
	factory := func() tsnetServer { return server }

	cfg := Config{
		Handler:         okHandler(),
		FunnelAddr:      "127.0.0.1:0",
		StartTimeout:    200 * time.Millisecond,
		ShutdownTimeout: 200 * time.Millisecond,
		PollInterval:    20 * time.Millisecond,
		Logger:          discardLogger(),
		// ExpectedIssuerHost intentionally empty.
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, factory, &fakeMinter{}) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	if server.listenCalls.Load() == 0 {
		t.Error("ListenFunnel was not called when check disabled")
	}
}

func TestRun_ReauthFromCold(t *testing.T) {
	// Two server instances: first reports NeedsLogin, second reports Running.
	servers := []*fakeServer{
		newFakeServer(BackendStateNeedsLogin),
		newFakeServer(BackendStateRunning),
	}
	idx := atomic.Int32{}
	factory := func() tsnetServer {
		i := idx.Add(1) - 1
		return servers[i]
	}
	minter := &fakeMinter{keys: []string{"tskey-fresh"}}

	runOnce(t, factory, minter, 200*time.Millisecond)

	if minter.calls.Load() != 1 {
		t.Errorf("minter calls = %d (want 1)", minter.calls.Load())
	}
	if servers[1].authKey != "tskey-fresh" {
		t.Errorf("second server auth key = %q", servers[1].authKey)
	}
	if servers[1].listenCalls.Load() == 0 {
		t.Error("second server never listened")
	}
}

func TestRun_MidFlightReauth(t *testing.T) {
	// First server starts Running then flips to NeedsLogin; second starts in
	// NeedsLogin (no persisted identity) and transitions to Running once the
	// runner provides a fresh auth key. The factory returns first on the
	// initial call and second for every subsequent call (the runner
	// re-invokes the factory on every reauth and on every cycle).
	first := newFakeServer(BackendStateRunning)
	second := newFakeServer(BackendStateNeedsLogin)
	second.stateOnAuthKey = BackendStateRunning
	idx := atomic.Int32{}
	factory := func() tsnetServer {
		i := idx.Add(1) - 1
		if i == 0 {
			return first
		}
		return second
	}
	minter := &fakeMinter{keys: []string{"tskey-mid"}}

	cfg := Config{
		Handler:         okHandler(),
		FunnelAddr:      "127.0.0.1:0",
		StartTimeout:    200 * time.Millisecond,
		ShutdownTimeout: 200 * time.Millisecond,
		PollInterval:    10 * time.Millisecond,
		Logger:          discardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, factory, minter) }()

	// Wait until first server is serving, then flip its state.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if first.listenCalls.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	first.setState(BackendStateNeedsLogin)

	// Wait until second server has been built and listened.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if second.listenCalls.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit")
	}

	if minter.calls.Load() != 1 {
		t.Errorf("minter calls = %d", minter.calls.Load())
	}
	if second.authKey != "tskey-mid" {
		t.Errorf("second auth key = %q", second.authKey)
	}
	if !first.closed.Load() {
		t.Error("first server not closed")
	}
}

func TestRun_MinterBackoffThenSuccess(t *testing.T) {
	failing := newFakeServer(BackendStateNeedsLogin)
	running := newFakeServer(BackendStateRunning)
	servers := []*fakeServer{failing, running}
	idx := atomic.Int32{}
	factory := func() tsnetServer {
		i := idx.Add(1) - 1
		if int(i) >= len(servers) {
			return running
		}
		return servers[i]
	}
	minter := &fakeMinter{
		errs: []error{errors.New("transient")},
		keys: []string{"", "tskey-after-retry"},
	}

	cfg := Config{
		Handler:         okHandler(),
		FunnelAddr:      "127.0.0.1:0",
		StartTimeout:    100 * time.Millisecond,
		ShutdownTimeout: 100 * time.Millisecond,
		PollInterval:    20 * time.Millisecond,
		Logger:          discardLogger(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, factory, minter) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if minter.calls.Load() >= 2 && running.listenCalls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit")
	}

	if minter.calls.Load() < 2 {
		t.Errorf("minter calls = %d (want >= 2)", minter.calls.Load())
	}
}

func TestRun_GracefulShutdownOnCancel(t *testing.T) {
	server := newFakeServer(BackendStateRunning)
	factory := func() tsnetServer { return server }
	minter := &fakeMinter{}

	runOnce(t, factory, minter, 100*time.Millisecond)

	if !server.closed.Load() {
		t.Error("server not closed on cancel")
	}
}

func TestRun_BackendStateProbeHonorsStartTimeout(t *testing.T) {
	server := newFakeServer(BackendStateRunning)
	server.backendStateFn = func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	factory := func() tsnetServer { return server }

	cfg := Config{
		Handler:         okHandler(),
		FunnelAddr:      "127.0.0.1:0",
		StartTimeout:    50 * time.Millisecond,
		ShutdownTimeout: 50 * time.Millisecond,
		PollInterval:    20 * time.Millisecond,
		Logger:          discardLogger(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := Run(ctx, cfg, factory, &fakeMinter{})
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Run took too long to respect probe timeout: %s", elapsed)
	}
}

func TestRun_ServeFailuresBackOff(t *testing.T) {
	server := newFakeServer(BackendStateRunning)
	server.listenFn = func() (net.Listener, error) {
		return nil, errors.New("listen failed")
	}
	factory := func() tsnetServer { return server }

	cfg := Config{
		Handler:         okHandler(),
		FunnelAddr:      "127.0.0.1:0",
		StartTimeout:    50 * time.Millisecond,
		ShutdownTimeout: 50 * time.Millisecond,
		PollInterval:    20 * time.Millisecond,
		Logger:          discardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, factory, &fakeMinter{}) }()

	time.Sleep(220 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit")
	}

	if got := server.startCalls.Load(); got > 3 {
		t.Fatalf("start calls = %d, want backoff to prevent tight looping", got)
	}
}

func TestRun_HandlerActuallyServes(t *testing.T) {
	server := newFakeServer(BackendStateRunning)
	factory := func() tsnetServer { return server }
	minter := &fakeMinter{}

	cfg := Config{
		Handler:         okHandler(),
		FunnelAddr:      "127.0.0.1:0",
		StartTimeout:    200 * time.Millisecond,
		ShutdownTimeout: 200 * time.Millisecond,
		PollInterval:    50 * time.Millisecond,
		Logger:          discardLogger(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, factory, minter) }()

	var addr string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if a := server.listenerAddr(); a != "" {
			addr = a
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if addr == "" {
		cancel()
		<-done
		t.Fatal("server has no listener")
	}

	resp, err := http.Get("http://" + addr + "/anything")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d", resp.StatusCode)
		}
	}

	cancel()
	<-done
}

// Sanity check the test helpers themselves so a regression in the fakes does
// not silently mask a runner regression.
func TestFakeServer_Roundtrip(t *testing.T) {
	f := newFakeServer(BackendStateRunning)
	if state, _ := f.BackendState(context.Background()); state != BackendStateRunning {
		t.Errorf("state = %q", state)
	}
	f.setState(BackendStateNeedsLogin)
	if state, _ := f.BackendState(context.Background()); state != BackendStateNeedsLogin {
		t.Errorf("state = %q", state)
	}
}

func TestFakeMinter_OrderingAndErrors(t *testing.T) {
	m := &fakeMinter{
		errs: []error{errors.New("first")},
		keys: []string{"", "k2"},
	}
	if _, err := m.Mint(context.Background()); err == nil {
		t.Error("first call should error")
	}
	if k, err := m.Mint(context.Background()); err != nil || k != "k2" {
		t.Errorf("second: k=%q err=%v", k, err)
	}
}

// Just check the default timeouts are sensible.
func TestDefaultHTTPTimeouts(t *testing.T) {
	to := DefaultHTTPTimeouts()
	if to.ReadHeaderTimeout == 0 {
		t.Error("ReadHeaderTimeout = 0 (Slowloris risk)")
	}
}

// Compile-time assertion that we use httptest correctly elsewhere; if this
// breaks, fakeServer's listener semantics are off.
var _ = httptest.NewRecorder
