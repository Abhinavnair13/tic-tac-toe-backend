package main

import (
	"context"
	"database/sql"
	"encoding/json"

	"tic-tac-toe/handlers"

	"github.com/heroiclabs/nakama-common/api" // NEW: Required for the hook payload
	"github.com/heroiclabs/nakama-common/runtime"
)

type DBStats struct {
	Wins   int `json:"wins"`
	Losses int `json:"losses"`
	Streak int `json:"streak"`
}

// A combined record ready for the leaderboard UI
type CombinedLeaderboardRecord struct {
	Rank     int64  `json:"rank"`
	Username string `json:"username"`
	UserID   string `json:"user_id"`
	Score    int64  `json:"trophies"`
	// Metadata  string  `json:"metadata"`   // Required by RPC type
	// ExpiresAt int64   `json:"expires_at"` // Required by RPC type
	Stats DBStats `json:"stats"` // <-- NEW: Our merged stats
}

func InitModule(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {
	logger.Info("Tic-Tac-Toe Plugin Loaded Successfully!")

	// 1. Initialize the Trophy Leaderboard
	err := nk.LeaderboardCreate(ctx, "global_trophies", true, "desc", "set", "", nil)
	if err != nil {
		logger.Error("Failed to create global_trophies leaderboard: %v", err)
	}

	// 2. NEW: Hook into Email Authentication to give 10 trophies to new accounts

	// 2. NEW: Hook into Email Authentication to give 10 trophies to new accounts
	err = initializer.RegisterAfterAuthenticateEmail(func(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, out *api.Session, in *api.AuthenticateEmailRequest) error {
		if out.Created {
			userID, ok := ctx.Value(runtime.RUNTIME_CTX_USER_ID).(string)
			if !ok {
				return nil
			}

			username, _ := ctx.Value(runtime.RUNTIME_CTX_USERNAME).(string)

			_, err := nk.LeaderboardRecordWrite(ctx, "global_trophies", userID, username, 10, 0, nil, nil)
			if err != nil {
				logger.Error("Failed to initialize trophies: %v", err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// 3. Register our pure Go game loop
	err = initializer.RegisterMatch("tictactoe_match", func(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule) (runtime.Match, error) {
		return &handlers.MatchHandler{}, nil
	})
	if err != nil {
		return err
	}

	// 4. Intercept the matchmaker and spawn the game loop
	err = initializer.RegisterMatchmakerMatched(func(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, entries []runtime.MatchmakerEntry) (string, error) {
		logger.Info("Matchmaker found 2 players! Spawning Authoritative Match...")

		matchId, err := nk.MatchCreate(ctx, "tictactoe_match", nil)
		if err != nil {
			return "", err
		}

		return matchId, nil
	})
	err = initializer.RegisterRpc("get_leaderboard_with_stats", func(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {

		// 1. Fetch top 50 records from Nakama
		records, _, _, _, err := nk.LeaderboardRecordsList(ctx, "global_trophies", []string{}, 50, "", 0)
		if err != nil {
			return "", err
		}

		// 2. Extract UserIDs to fetch their storage data in one bulk operation
		userIds := make([]string, 0, len(records))
		storageReads := make([]*runtime.StorageRead, 0, len(records))
		for _, r := range records {
			userIds = append(userIds, r.OwnerId)
			storageReads = append(storageReads, &runtime.StorageRead{
				Collection: "stats",
				Key:        "profile",
				UserID:     r.OwnerId,
			})
		}

		// 3. Bulk read storage stats safely
		var statsObjects []*api.StorageObject
		if len(storageReads) > 0 {
			statsObjects, err = nk.StorageRead(ctx, storageReads)
			if err != nil {
				logger.Error("Storage read error: %v", err)
				// Don't return, just continue with empty stats!
			}
		}

		// 4. Map Stats to UserIDs
		statsMap := make(map[string]DBStats)
		for _, obj := range statsObjects {
			var s DBStats
			if json.Unmarshal([]byte(obj.Value), &s) == nil {
				statsMap[obj.UserId] = s
			}
		}

		// 5. Create final combined list safely
		finalList := make([]CombinedLeaderboardRecord, 0, len(records))
		for _, r := range records {

			// THE FIX: Safely extract username without crashing on old records
			uname := "Anonymous"
			if r.GetUsername() != nil {
				uname = r.GetUsername().GetValue()
			}

			finalList = append(finalList, CombinedLeaderboardRecord{
				Rank:     r.Rank,
				Username: uname,
				UserID:   r.OwnerId,
				Score:    r.Score,
				// Metadata:  r.Metadata,
				// ExpiresAt: r.GetExpiryTime().GetSeconds(),
				Stats: statsMap[r.OwnerId],
			})
		}
		logger.Info("Leaderboard: %v", finalList)
		// 6. JSON Encode and return to frontend
		responseBytes, _ := json.Marshal(finalList)
		return string(responseBytes), nil
	})
	if err != nil {
		return err
	}

	return nil
}
