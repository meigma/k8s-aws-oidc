package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meigma/k8s-aws-oidc/internal/config"
	"github.com/meigma/k8s-aws-oidc/internal/leader"
	"github.com/meigma/k8s-aws-oidc/internal/metrics"
)

func testLeaderConfig() *config.Config {
	return &config.Config{
		LeaderElectionEnabled:       true,
		LeaderElectionLeaseName:     "oidc-lease",
		LeaderElectionNamespace:     "oidc-system",
		LeaderElectionIdentity:      "oidc-pod-0",
		LeaderElectionLeaseDuration: 15 * time.Second,
		LeaderElectionRenewDeadline: 10 * time.Second,
		LeaderElectionRetryPeriod:   2 * time.Second,
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunWithLeaderElection_LeaderInvokesPublicRunner(t *testing.T) {
	cfg := testLeaderConfig()
	state := &runtimeState{}
	state.SetLeaderElectionEnabled(true)
	recorder := metrics.New(time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	started := make(chan struct{}, 1)
	publicRunner := func(ctx context.Context) error {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil
	}
	leaderRun := func(ctx context.Context, cfg leader.Config) error {
		runCtx, cancelRun := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			cfg.OnStartedLeading(runCtx)
			close(done)
		}()
		<-started
		cancel()
		cancelRun()
		<-done
		cfg.OnStoppedLeading()
		return nil
	}

	err := runWithLeaderElection(ctx, cfg, state, testLogger(), recorder, leaderRun, publicRunner)
	if err != nil {
		t.Fatalf("runWithLeaderElection: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("public runner calls = %d, want 1", calls.Load())
	}
	if got := recorder.Registry(); got == nil {
		t.Fatal("metrics registry is nil")
	}
}

func TestRunWithLeaderElection_FollowerDoesNotInvokePublicRunner(t *testing.T) {
	cfg := testLeaderConfig()
	state := &runtimeState{}
	state.SetLeaderElectionEnabled(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	publicRunner := func(context.Context) error {
		calls.Add(1)
		return nil
	}
	leaderRun := func(ctx context.Context, cfg leader.Config) error {
		cfg.OnNewLeader("oidc-pod-1")
		cancel()
		<-ctx.Done()
		return nil
	}

	err := runWithLeaderElection(ctx, cfg, state, testLogger(), nil, leaderRun, publicRunner)
	if err != nil {
		t.Fatalf("runWithLeaderElection: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("public runner calls = %d, want 0", calls.Load())
	}
}

func TestRunWithLeaderElection_LostLeadershipClearsStateAndReturnsError(t *testing.T) {
	cfg := testLeaderConfig()
	state := &runtimeState{}
	state.SetLeaderElectionEnabled(true)
	state.SetPublicReady(true)
	recorder := metrics.New(time.Minute)

	started := make(chan struct{}, 1)
	publicRunner := func(ctx context.Context) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil
	}
	leaderRun := func(_ context.Context, cfg leader.Config) error {
		runCtx, cancelRun := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			cfg.OnStartedLeading(runCtx)
			close(done)
		}()
		<-started
		cancelRun()
		<-done
		cfg.OnStoppedLeading()
		return nil
	}

	err := runWithLeaderElection(context.Background(), cfg, state, testLogger(), recorder, leaderRun, publicRunner)
	if !errors.Is(err, errLeadershipLost) {
		t.Fatalf("runWithLeaderElection error = %v, want %v", err, errLeadershipLost)
	}
	if state.Leader() {
		t.Fatal("leader state not cleared")
	}
	if state.PublicReady() {
		t.Fatal("publicReady state not cleared")
	}
}

func TestRunWithLeaderElection_PublicRunnerFailureReturnsError(t *testing.T) {
	cfg := testLeaderConfig()
	state := &runtimeState{}
	state.SetLeaderElectionEnabled(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wantErr := errors.New("boom")
	runnerDone := make(chan struct{}, 1)
	publicRunner := func(context.Context) error {
		select {
		case runnerDone <- struct{}{}:
		default:
		}
		return wantErr
	}
	leaderRun := func(ctx context.Context, cfg leader.Config) error {
		go cfg.OnStartedLeading(ctx)
		<-runnerDone
		<-ctx.Done()
		return nil
	}

	err := runWithLeaderElection(ctx, cfg, state, testLogger(), nil, leaderRun, publicRunner)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithLeaderElection error = %v, want %v", err, wantErr)
	}
}
