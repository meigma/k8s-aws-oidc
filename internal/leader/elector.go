package leader

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

const (
	defaultLeaseDuration = 15 * time.Second
	defaultRenewDeadline = 10 * time.Second
	defaultRetryPeriod   = 2 * time.Second
)

// Config configures Kubernetes Lease-based leader election.
type Config struct {
	LeaseName     string
	Namespace     string
	Identity      string
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
	Logger        *slog.Logger

	OnStartedLeading func(context.Context)
	OnStoppedLeading func()
	OnNewLeader      func(string)
}

func (c *Config) defaults() {
	if c.LeaseDuration == 0 {
		c.LeaseDuration = defaultLeaseDuration
	}
	if c.RenewDeadline == 0 {
		c.RenewDeadline = defaultRenewDeadline
	}
	if c.RetryPeriod == 0 {
		c.RetryPeriod = defaultRetryPeriod
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.OnStartedLeading == nil {
		c.OnStartedLeading = func(context.Context) {}
	}
	if c.OnStoppedLeading == nil {
		c.OnStoppedLeading = func() {}
	}
	if c.OnNewLeader == nil {
		c.OnNewLeader = func(string) {}
	}
}

func (c *Config) validate() error {
	if c.LeaseName == "" {
		return errors.New("leader election: lease name is required")
	}
	if c.Namespace == "" {
		return errors.New("leader election: namespace is required")
	}
	if c.Identity == "" {
		return errors.New("leader election: identity is required")
	}
	if c.LeaseDuration <= 0 {
		return fmt.Errorf("leader election: lease duration must be positive, got %s", c.LeaseDuration)
	}
	if c.RenewDeadline <= 0 {
		return fmt.Errorf("leader election: renew deadline must be positive, got %s", c.RenewDeadline)
	}
	if c.RetryPeriod <= 0 {
		return fmt.Errorf("leader election: retry period must be positive, got %s", c.RetryPeriod)
	}
	if c.LeaseDuration <= c.RenewDeadline {
		return fmt.Errorf("leader election: lease duration %s must be greater than renew deadline %s", c.LeaseDuration, c.RenewDeadline)
	}
	if c.RenewDeadline <= c.RetryPeriod {
		return fmt.Errorf("leader election: renew deadline %s must be greater than retry period %s", c.RenewDeadline, c.RetryPeriod)
	}
	return nil
}

// Run joins the configured leader election and blocks until ctx is canceled.
func Run(ctx context.Context, cfg Config) error {
	cfg.defaults()
	if err := cfg.validate(); err != nil {
		return err
	}

	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("leader election in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("leader election clientset: %w", err)
	}

	lock, err := resourcelock.New(
		resourcelock.LeasesResourceLock,
		cfg.Namespace,
		cfg.LeaseName,
		clientset.CoreV1(),
		clientset.CoordinationV1(),
		resourcelock.ResourceLockConfig{
			Identity: cfg.Identity,
		},
	)
	if err != nil {
		return fmt.Errorf("leader election lock: %w", err)
	}

	elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   cfg.LeaseDuration,
		RenewDeadline:   cfg.RenewDeadline,
		RetryPeriod:     cfg.RetryPeriod,
		Name:            cfg.LeaseName,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: cfg.OnStartedLeading,
			OnStoppedLeading: cfg.OnStoppedLeading,
			OnNewLeader:      cfg.OnNewLeader,
		},
	})
	if err != nil {
		return fmt.Errorf("leader election elector: %w", err)
	}

	elector.Run(ctx)
	return nil
}
