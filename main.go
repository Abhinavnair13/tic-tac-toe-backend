package main

import (
	"context"
	"database/sql"

	"tic-tac-toe/handlers"

	"github.com/heroiclabs/nakama-common/runtime"
)

func InitModule(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {
	logger.Info("Tic-Tac-Toe Plugin Loaded Successfully!")

	// 1. NEW: Initialize the Trophy Leaderboard
	// authoritative = true (clients cannot cheat), sort = desc, operator = "set" (allows score to drop)
	err := nk.LeaderboardCreate(ctx, "global_trophies", true, "desc", "set", "", nil)
	if err != nil {
		logger.Error("Failed to create global_trophies leaderboard: %v", err)
	}

	// 2. Register our pure Go game loop
	err = initializer.RegisterMatch("tictactoe_match", func(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule) (runtime.Match, error) {
		return &handlers.MatchHandler{}, nil
	})
	if err != nil {
		return err
	}

	// 3. Intercept the matchmaker and spawn the game loop
	err = initializer.RegisterMatchmakerMatched(func(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, entries []runtime.MatchmakerEntry) (string, error) {
		logger.Info("Matchmaker found 2 players! Spawning Authoritative Match...")

		matchId, err := nk.MatchCreate(ctx, "tictactoe_match", nil)
		if err != nil {
			return "", err
		}

		return matchId, nil
	})
	if err != nil {
		return err
	}

	return nil
}
