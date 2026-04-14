package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/meigma/k8s-aws-oidc/internal/config"
	"github.com/meigma/k8s-aws-oidc/internal/leader"
	"github.com/meigma/k8s-aws-oidc/internal/logx"
	"github.com/meigma/k8s-aws-oidc/internal/metrics"
)

var errLeadershipLost = errors.New("leadership lost")

type leaderRunFunc func(context.Context, leader.Config) error

func runWithLeaderElection(
	ctx context.Context,
	cfg *config.Config,
	state *runtimeState,
	logger *slog.Logger,
	recorder *metrics.Metrics,
	runLeaderElection leaderRunFunc,
	publicRunner func(context.Context) error,
) error {
	errCh := make(chan error, 1)
	var once sync.Once
	var deferredErrMu sync.Mutex
	var deferredErr error
	sendErr := func(err error) {
		if err == nil {
			return
		}
		deferredErrMu.Lock()
		if deferredErr == nil {
			deferredErr = err
		}
		deferredErrMu.Unlock()
		once.Do(func() {
			errCh <- err
		})
	}

	leaderCfg := leader.Config{
		LeaseName:     cfg.LeaderElectionLeaseName,
		Namespace:     cfg.LeaderElectionNamespace,
		Identity:      cfg.LeaderElectionIdentity,
		LeaseDuration: cfg.LeaderElectionLeaseDuration,
		RenewDeadline: cfg.LeaderElectionRenewDeadline,
		RetryPeriod:   cfg.LeaderElectionRetryPeriod,
		Logger:        logger,
		OnStartedLeading: func(runCtx context.Context) {
			state.SetLeader(true)
			if recorder != nil {
				recorder.SetLeader(true)
				recorder.RecordLeaderElectionTransition("leader")
			}
			logLeadershipAcquired(runCtx, logger, cfg.LeaderElectionIdentity, cfg.LeaderElectionLeaseName)
			if err := publicRunner(runCtx); err != nil && !errors.Is(err, context.Canceled) && runCtx.Err() == nil {
				logLeaderRunnerExit(runCtx, logger, err)
				sendErr(err)
			}
		},
		OnStoppedLeading: func() {
			state.SetLeader(false)
			state.SetPublicReady(false)
			if recorder != nil {
				recorder.SetLeader(false)
				recorder.SetPublicReady(false)
				recorder.RecordLeaderElectionTransition("follower")
			}
			if state.ShuttingDown() || ctx.Err() != nil {
				return
			}
			logLeadershipLost(ctx, logger, cfg.LeaderElectionIdentity, cfg.LeaderElectionLeaseName)
			sendErr(errLeadershipLost)
		},
		OnNewLeader: func(identity string) {
			logObservedLeader(ctx, logger, cfg.LeaderElectionIdentity, identity)
		},
	}

	logLeaderElectionInitialized(ctx, logger, leaderCfg)
	electionErrCh := make(chan error, 1)
	go func() {
		electionErrCh <- runLeaderElection(ctx, leaderCfg)
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	case err := <-electionErrCh:
		if err == nil && ctx.Err() == nil {
			deferredErrMu.Lock()
			err = deferredErr
			deferredErrMu.Unlock()
		}
		return err
	}
}

func logLeaderElectionInitialized(ctx context.Context, logger *slog.Logger, cfg leader.Config) {
	logx.Info(ctx, logger, "leader_election", "leader_election_initialized", "leader election initialized",
		slog.String("lease_name", cfg.LeaseName),
		slog.String("namespace", cfg.Namespace),
		slog.String("identity", cfg.Identity),
		slog.Duration("lease_duration", cfg.LeaseDuration),
		slog.Duration("renew_deadline", cfg.RenewDeadline),
		slog.Duration("retry_period", cfg.RetryPeriod),
	)
}

func logLeadershipAcquired(ctx context.Context, logger *slog.Logger, identity, leaseName string) {
	logx.Info(ctx, logger, "leader_election", "leadership_acquired", "leadership acquired",
		slog.String("identity", identity),
		slog.String("lease_name", leaseName),
	)
}

func logLeadershipLost(ctx context.Context, logger *slog.Logger, identity, leaseName string) {
	logx.Warn(ctx, logger, "leader_election", "leadership_lost", "leadership lost",
		slog.String("identity", identity),
		slog.String("lease_name", leaseName),
	)
}

func logObservedLeader(ctx context.Context, logger *slog.Logger, identity, observed string) {
	if observed == "" || observed == identity {
		return
	}
	logx.Info(ctx, logger, "leader_election", "leader_observed", "observed new leader",
		slog.String("identity", identity),
		slog.String("leader_identity", observed),
	)
}

func logLeaderRunnerExit(ctx context.Context, logger *slog.Logger, err error) {
	logx.Error(ctx, logger, "leader_election", "leader_runner_exit", "leader runner exited with error",
		slog.String("error_kind", processErrorKind(err)),
		slog.String("error", processErrorSummary(err)),
	)
}
