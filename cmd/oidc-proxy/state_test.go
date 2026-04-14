package main

import "testing"

func TestRuntimeStateReadiness(t *testing.T) {
	tests := []struct {
		name                  string
		leaderElectionEnabled bool
		leader                bool
		publicReady           bool
		jwksReady             bool
		wantReady             bool
		wantLeaderReady       bool
	}{
		{
			name:            "single replica not ready until public ready",
			leader:          true,
			publicReady:     false,
			jwksReady:       true,
			wantReady:       false,
			wantLeaderReady: false,
		},
		{
			name:            "single replica ready when public ready",
			leader:          true,
			publicReady:     true,
			jwksReady:       true,
			wantReady:       true,
			wantLeaderReady: true,
		},
		{
			name:                  "ha follower stays ready while standby",
			leaderElectionEnabled: true,
			leader:                false,
			publicReady:           false,
			jwksReady:             true,
			wantReady:             true,
			wantLeaderReady:       false,
		},
		{
			name:                  "ha leader not ready until public listener ready",
			leaderElectionEnabled: true,
			leader:                true,
			publicReady:           false,
			jwksReady:             true,
			wantReady:             false,
			wantLeaderReady:       false,
		},
		{
			name:                  "ha leader ready when public listener ready",
			leaderElectionEnabled: true,
			leader:                true,
			publicReady:           true,
			jwksReady:             true,
			wantReady:             true,
			wantLeaderReady:       true,
		},
		{
			name:                  "jwks gates readiness in all modes",
			leaderElectionEnabled: true,
			leader:                false,
			publicReady:           false,
			jwksReady:             false,
			wantReady:             false,
			wantLeaderReady:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var state runtimeState
			state.SetLeaderElectionEnabled(tt.leaderElectionEnabled)
			state.SetLeader(tt.leader)
			state.SetPublicReady(tt.publicReady)

			if got := state.Ready(tt.jwksReady); got != tt.wantReady {
				t.Fatalf("Ready() = %v, want %v", got, tt.wantReady)
			}
			if got := state.LeaderReady(tt.jwksReady); got != tt.wantLeaderReady {
				t.Fatalf("LeaderReady() = %v, want %v", got, tt.wantLeaderReady)
			}
		})
	}
}
