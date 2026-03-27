package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"tic-tac-toe/api"
	"tic-tac-toe/core"
	"time"

	"github.com/heroiclabs/nakama-common/runtime"
	"google.golang.org/protobuf/proto"
)

type MatchHandler struct{}

type MatchState struct {
	Game             *core.Game
	Presences        map[string]runtime.Presence
	InitialStateSent bool
	Mode             string
}

func (m *MatchHandler) MatchInit(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, params map[string]interface{}) (interface{}, int, string) {
	mode := "timed"
	if params != nil && params["mode"] != nil {
		mode = params["mode"].(string)
	}

	state := &MatchState{
		Presences: make(map[string]runtime.Presence),
		Mode:      mode,
	}
	return state, 10, "tictactoe"
}

func (m *MatchHandler) MatchJoin(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presences []runtime.Presence) interface{} {
	s := state.(*MatchState)
	for _, p := range presences {
		s.Presences[p.GetUserId()] = p

		if s.Game != nil {
			s.Game.PlayerReconnected(p.GetUserId())
			logger.Info("Player %s reconnected. Resuming match.", p.GetUserId())
		}
	}

	if len(s.Presences) == 2 && s.Game == nil {
		var p1, p2 string
		for id := range s.Presences {
			if p1 == "" {
				p1 = id
			} else {
				p2 = id
			}
		}
		isTimed := s.Mode == "timed"
		s.Game = core.NewGame(p1, p2, isTimed)

		matchID := ctx.Value(runtime.RUNTIME_CTX_MATCH_ID).(string)
		setActiveMatch(ctx, logger, nk, p1, matchID)
		setActiveMatch(ctx, logger, nk, p2, matchID)

		if isTimed {
			logger.Info("Game Started between %s and %s in Timed Mode", p1, p2)
		} else {
			logger.Info("Game Started between %s and %s in Classic Mode", p1, p2)
		}
	}

	// Resync state to everyone (especially the rejoiner to hide the warning modal)
	if s.Game != nil {
		broadcastState(s, dispatcher)
	}

	return s
}

func (m *MatchHandler) MatchLeave(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presences []runtime.Presence) interface{} {
	s := state.(*MatchState)

	for _, p := range presences {
		leaverID := p.GetUserId()
		delete(s.Presences, leaverID)

		if s.Game != nil && s.Game.WinnerID == "" {
			s.Game.PlayerDisconnected(leaverID, time.Now().Unix())
			logger.Info("Player %s disconnected. Grace period logic delegated to core.", leaverID)
			broadcastState(s, dispatcher)
		}
	}

	if len(s.Presences) == 0 && (s.Game == nil || s.Game.WinnerID != "") {
		return nil
	}

	return s
}

func (m *MatchHandler) MatchLoop(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, messages []runtime.MatchData) interface{} {
	s := state.(*MatchState)
	if s.Game == nil {
		return s
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Error("🚨 CRITICAL PANIC IN MATCHLOOP: %v", r)
		}
	}()

	if !s.InitialStateSent {
		broadcastState(s, dispatcher)
		s.InitialStateSent = true
	}

	now := time.Now().Unix()

	// 1. Let the Core Game handle time-based logic (timeouts, disconnects)
	stateChanged, gameOver := s.Game.Update(now)

	// 2. Process incoming messages
	if !gameOver {
		for _, msg := range messages {
			opCode := msg.GetOpCode()

			if opCode == 4 { // Ping
				dispatcher.BroadcastMessage(4, msg.GetData(), []runtime.Presence{msg}, nil, true)
				continue
			}

			if opCode == 1 { // Move
				var moveReq api.MoveRequest
				if err := proto.Unmarshal(msg.GetData(), &moveReq); err != nil {
					continue
				}

				if s.Game.AttemptMove(logger, msg.GetUserId(), moveReq.Position) {
					stateChanged = true

					// 3. Check for standard Win/Draw after a move
					if s.Game.CheckWin() || s.Game.CheckDraw() {
						gameOver = true
					}
				}
			}
		}
	}

	// 4. Handle Rewards & Terminate if game ended
	if stateChanged {
		broadcastState(s, dispatcher)

		if gameOver {
			if s.Game.WinnerID != "" {
				loserID := s.Game.GetLoserID()
				winnerTime := s.Game.GetWinnerTimeUsed()
				updateTrophies(ctx, logger, nk, s.Game.WinnerID, loserID, winnerTime, s.Game.IsTimedMode)
				updateMatchStats(ctx, logger, nk, s.Game.WinnerID, loserID)
			}

			clearActiveMatch(ctx, logger, nk, s.Game.Player1ID)
			clearActiveMatch(ctx, logger, nk, s.Game.Player2ID)
			return nil // End the server match loop gracefully
		}
	}

	return s
}
func (m *MatchHandler) MatchJoinAttempt(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presence runtime.Presence, metadata map[string]string) (interface{}, bool, string) {
	s := state.(*MatchState)
	if len(s.Presences) >= 2 {
		return s, false, "Match is full"
	}
	return s, true, ""
}

func (m *MatchHandler) MatchTerminate(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, graceSeconds int) interface{} {
	return state
}

func (m *MatchHandler) MatchSignal(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, data string) (interface{}, string) {
	return state, ""
}

func updateTrophies(ctx context.Context, logger runtime.Logger, nk runtime.NakamaModule, winnerID string, loserID string, winnerTimeUsed int64, isTimedMode bool) { // 1. UPDATE WINNER (+10)
	var winnerScore int64 = 0
	var winnerName string = ""
	var trophiesToAward int64 = 10
	logger.Info("Winner total time used %d", winnerTimeUsed)
	if !isTimedMode {
		if winnerTimeUsed <= 60 {
			trophiesToAward = 10
		} else if winnerTimeUsed <= 90 {
			trophiesToAward = 9
		} else if winnerTimeUsed <= 120 {
			trophiesToAward = 8
		} else {
			trophiesToAward = 7
		}
	}
	_, ownerRecords, _, _, err := nk.LeaderboardRecordsList(ctx, "global_trophies", []string{winnerID}, 1, "", 0)
	if err == nil && len(ownerRecords) > 0 {
		winnerScore = ownerRecords[0].Score
		if ownerRecords[0].GetUsername() != nil {
			winnerName = ownerRecords[0].GetUsername().GetValue()
		}
	}

	_, err = nk.LeaderboardRecordWrite(ctx, "global_trophies", winnerID, winnerName, winnerScore+trophiesToAward, 0, nil, nil)
	if err != nil {
		logger.Error("Error writing winner trophies: %v", err)
	}

	// 2. UPDATE LOSER (-2)
	var loserScore int64 = 0
	var loserName string = ""

	_, loserOwnerRecords, _, _, err := nk.LeaderboardRecordsList(ctx, "global_trophies", []string{loserID}, 1, "", 0)
	if err == nil && len(loserOwnerRecords) > 0 {
		loserScore = loserOwnerRecords[0].Score
		if loserOwnerRecords[0].GetUsername() != nil {
			loserName = loserOwnerRecords[0].GetUsername().GetValue() // Grab the existing username!
		}
	}

	if loserScore > 2 {
		_, err = nk.LeaderboardRecordWrite(ctx, "global_trophies", loserID, loserName, loserScore-2, 0, nil, nil)
	}
}

type PlayerStats struct {
	Wins   int `json:"wins"`
	Losses int `json:"losses"`
	Streak int `json:"streak"`
}

func updateMatchStats(ctx context.Context, logger runtime.Logger, nk runtime.NakamaModule, winnerID string, loserID string) {
	updateSinglePlayerStat(ctx, logger, nk, winnerID, true)
	if loserID != "" {
		updateSinglePlayerStat(ctx, logger, nk, loserID, false)
	}
}

func updateSinglePlayerStat(ctx context.Context, logger runtime.Logger, nk runtime.NakamaModule, userID string, isWinner bool) {
	var stats PlayerStats

	// 1. Read existing stats from Nakama Storage
	records, err := nk.StorageRead(ctx, []*runtime.StorageRead{
		{Collection: "stats", Key: "profile", UserID: userID},
	})

	if err == nil && len(records) > 0 {
		json.Unmarshal([]byte(records[0].Value), &stats)
	}

	// 2. Modify the stats
	if isWinner {
		stats.Wins++
		stats.Streak++
	} else {
		stats.Losses++
		stats.Streak = 0 // Reset streak on loss
	}

	// 3. Write them back (PermissionRead: 2 means public can view, PermissionWrite: 0 means clients CANNOT cheat/edit)
	bytes, _ := json.Marshal(stats)
	_, err = nk.StorageWrite(ctx, []*runtime.StorageWrite{
		{
			Collection:      "stats",
			Key:             "profile",
			UserID:          userID,
			Value:           string(bytes),
			PermissionRead:  2,
			PermissionWrite: 0,
		},
	})

	if err != nil {
		logger.Error("Failed to write stats for user %s: %v", userID, err)
	}
}
func broadcastState(s *MatchState, dispatcher runtime.MatchDispatcher) {
	if s.Game == nil {
		return
	}
	protoState := &api.GameState{
		Board:            s.Game.Board,
		CurrentTurn:      s.Game.CurrentTurn,
		TurnStartTime:    s.Game.TurnStartTime,
		WinnerId:         s.Game.WinnerID,
		P1Id:             s.Game.Player1ID,
		P2Id:             s.Game.Player2ID,
		IsTimedMode:      s.Game.IsTimedMode,
		P1TimeUsed:       s.Game.P1TimeUsed,
		P2TimeUsed:       s.Game.P2TimeUsed,
		P1DisconnectTime: s.Game.P1DisconnectTime,
		P2DisconnectTime: s.Game.P2DisconnectTime,
	}
	outBytes, _ := proto.Marshal(protoState)
	dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_STATE), outBytes, nil, nil, true)
}

func setActiveMatch(ctx context.Context, logger runtime.Logger, nk runtime.NakamaModule, userID, matchID string) {
	bytes, _ := json.Marshal(map[string]string{"match_id": matchID})
	nk.StorageWrite(ctx, []*runtime.StorageWrite{{
		Collection:      "system",
		Key:             "active_match",
		UserID:          userID,
		Value:           string(bytes),
		PermissionRead:  1, // 1 = Owner can read it (Frontend), 0 = Nobody can edit it
		PermissionWrite: 0,
	}})
	logger.Info("Active match set for user: %s with matchId: %s", userID, matchID)
}

func clearActiveMatch(ctx context.Context, logger runtime.Logger, nk runtime.NakamaModule, userID string) {
	nk.StorageDelete(ctx, []*runtime.StorageDelete{{
		Collection: "system",
		Key:        "active_match",
		UserID:     userID,
	}})
	logger.Info("Active match cleared for user: %s with matchId :%s", userID)
}
