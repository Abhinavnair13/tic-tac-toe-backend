package handlers

import (
	"context"
	"database/sql"

	"github.com/heroiclabs/nakama-common/api"
	"github.com/heroiclabs/nakama-common/runtime"
)

// AfterAuthenticateEmailHook is now a method of GameHandlers
func (h *GameHandlers) AfterAuthenticateEmailHook(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, out *api.Session, in *api.AuthenticateEmailRequest) error {
	if out.Created {
		userID, ok := ctx.Value(runtime.RUNTIME_CTX_USER_ID).(string)
		if !ok {
			return nil
		}

		username, _ := ctx.Value(runtime.RUNTIME_CTX_USERNAME).(string)

		// Note: We could use h.nk here, but Nakama safely injects it into the function arguments anyway!
		_, err := nk.LeaderboardRecordWrite(ctx, "global_trophies", userID, username, 10, 0, nil, nil)
		if err != nil {
			logger.Error("Failed to initialize trophies: %v", err)
		}
	}
	return nil
}

// MatchmakerMatchedHook is now a method of GameHandlers
func (h *GameHandlers) MatchmakerMatchedHook(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, entries []runtime.MatchmakerEntry) (string, error) {
	logger.Info("Matchmaker found 2 players! Spawning Authoritative Match...")

	mode := "timed"
	if len(entries) > 0 {
		if props := entries[0].GetProperties(); props != nil {
			if val, ok := props["mode"].(string); ok {
				mode = val
			}
		}
	}

	matchId, err := nk.MatchCreate(ctx, "tictactoe_match", map[string]interface{}{"mode": mode})
	if err != nil {
		return "", err
	}

	return matchId, nil
}
