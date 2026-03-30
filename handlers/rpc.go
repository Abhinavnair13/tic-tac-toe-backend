package handlers

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/heroiclabs/nakama-common/api"
	"github.com/heroiclabs/nakama-common/runtime"
)

type DBStats struct {
	Wins   int `json:"wins"`
	Losses int `json:"losses"`
	Streak int `json:"streak"`
}

type CombinedLeaderboardRecord struct {
	Rank     int64   `json:"rank"`
	Username string  `json:"username"`
	UserID   string  `json:"user_id"`
	Score    int64   `json:"trophies"`
	Stats    DBStats `json:"stats"`
}

func (h *GameHandlers) GetLeaderboardWithStatsRPC(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	records, _, _, _, err := nk.LeaderboardRecordsList(ctx, "global_trophies", []string{}, 50, "", 0)
	if err != nil {
		return "", err
	}

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

	var statsObjects []*api.StorageObject
	if len(storageReads) > 0 {
		statsObjects, err = nk.StorageRead(ctx, storageReads)
		if err != nil {
			logger.Error("Storage read error: %v", err)
		}
	}

	statsMap := make(map[string]DBStats)
	for _, obj := range statsObjects {
		var s DBStats
		if json.Unmarshal([]byte(obj.Value), &s) == nil {
			statsMap[obj.UserId] = s
		}
	}

	finalList := make([]CombinedLeaderboardRecord, 0, len(records))
	for _, r := range records {
		uname := "Anonymous"
		if r.GetUsername() != nil {
			uname = r.GetUsername().GetValue()
		}

		finalList = append(finalList, CombinedLeaderboardRecord{
			Rank:     r.Rank,
			Username: uname,
			UserID:   r.OwnerId,
			Score:    r.Score,
			Stats:    statsMap[r.OwnerId],
		})
	}

	responseBytes, _ := json.Marshal(finalList)
	return string(responseBytes), nil
}
