package main

import (
	"context"
	"database/sql"

	"tic-tac-toe/handlers"

	"github.com/heroiclabs/nakama-common/runtime"
)

func InitModule(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {
	logger.Info("Tic-Tac-Toe Plugin Loaded Successfully!")

	err := nk.LeaderboardCreate(ctx, "global_trophies", true, "desc", "set", "", nil)
	if err != nil {
		logger.Error("Failed to create global_trophies leaderboard: %v", err)
	}

	gh := handlers.NewGameHandlers(nk)
	if err := initializer.RegisterAfterAuthenticateEmail(gh.AfterAuthenticateEmailHook); err != nil {
		return err
	}
	if err := initializer.RegisterMatchmakerMatched(gh.MatchmakerMatchedHook); err != nil {
		return err
	}

	err = initializer.RegisterMatch("tictactoe_match", func(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule) (runtime.Match, error) {
		return &handlers.MatchHandler{}, nil
	})
	if err != nil {
		return err
	}

	if err := initializer.RegisterRpc("get_leaderboard_with_stats", gh.GetLeaderboardWithStatsRPC); err != nil {
		return err
	}

	return nil
}
