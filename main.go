package main

import (
	"context"
	"database/sql"

	"tic-tac-toe/handlers"

	"github.com/heroiclabs/nakama-common/runtime"
)

// InitModule is the initialization point for Nakama plugins.
func InitModule(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {
	logger.Info("Tic-Tac-Toe Plugin Loaded Successfully!")

	// Register our authoritative match handler
	err := initializer.RegisterMatch("tictactoe_match", func(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule) (runtime.Match, error) {
		return &handlers.MatchHandler{}, nil
	})

	if err != nil {
		logger.Error("Unable to register match: %v", err)
		return err
	}

	return nil
}
