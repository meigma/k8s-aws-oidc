package main

import "sync/atomic"

type runtimeState struct {
	leaderElectionEnabled atomic.Bool
	leader                atomic.Bool
	publicReady           atomic.Bool
	shuttingDown          atomic.Bool
}

func (s *runtimeState) SetLeaderElectionEnabled(v bool) {
	s.leaderElectionEnabled.Store(v)
}

func (s *runtimeState) LeaderElectionEnabled() bool {
	return s.leaderElectionEnabled.Load()
}

func (s *runtimeState) SetLeader(v bool) {
	s.leader.Store(v)
}

func (s *runtimeState) Leader() bool {
	return s.leader.Load()
}

func (s *runtimeState) SetPublicReady(v bool) {
	s.publicReady.Store(v)
}

func (s *runtimeState) PublicReady() bool {
	return s.publicReady.Load()
}

func (s *runtimeState) SetShuttingDown(v bool) {
	s.shuttingDown.Store(v)
}

func (s *runtimeState) ShuttingDown() bool {
	return s.shuttingDown.Load()
}

func (s *runtimeState) Live() bool {
	return true
}

func (s *runtimeState) Ready(jwksReady bool) bool {
	if !jwksReady {
		return false
	}
	if !s.LeaderElectionEnabled() {
		return s.PublicReady()
	}
	return !s.Leader() || s.PublicReady()
}

func (s *runtimeState) LeaderReady(jwksReady bool) bool {
	if !jwksReady || !s.PublicReady() {
		return false
	}
	if !s.LeaderElectionEnabled() {
		return true
	}
	return s.Leader()
}
