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

func (m *MatchHandler) MatchJoinAttempt(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presence runtime.Presence, metadata map[string]string) (interface{}, bool, string) {
	s := state.(*MatchState)
	if len(s.Presences) >= 2 {
		return s, false, "Match is full"
	}
	return s, true, ""
}

func (m *MatchHandler) MatchJoin(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presences []runtime.Presence) interface{} {
	s := state.(*MatchState)
	for _, p := range presences {
		s.Presences[p.GetUserId()] = p

		// If returning to an active game, clear the disconnect timer
		if s.Game != nil {
			if p.GetUserId() == s.Game.Player1ID {
				s.Game.P1DisconnectTime = 0
				logger.Info("Player 1 reconnected. Resuming match.")
			} else if p.GetUserId() == s.Game.Player2ID {
				s.Game.P2DisconnectTime = 0
				logger.Info("Player 2 reconnected. Resuming match.")
			}
		}
	}

	// Start brand new game if it hasn't started
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

		// If the game is active, DO NOT forfeit. Start the disconnect timer.
		if s.Game != nil && s.Game.WinnerID == "" {
			if leaverID == s.Game.Player1ID {
				s.Game.P1DisconnectTime = time.Now().Unix()
				logger.Info("Player 1 disconnected. Starting 15s grace period.")
			} else if leaverID == s.Game.Player2ID {
				s.Game.P2DisconnectTime = time.Now().Unix()
				logger.Info("Player 2 disconnected. Starting 15s grace period.")
			}

			// Broadcast the state so the remaining player sees the modal
			broadcastState(s, dispatcher)
		}
	}

	// Only close the match instance if everyone is gone AND the game is actually over/empty
	if len(s.Presences) == 0 && (s.Game == nil || s.Game.WinnerID != "") {
		return nil
	}

	return s // Keep match alive in memory!
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

	// 1. Broadcast Initial State
	if !s.InitialStateSent {
		broadcastState(s, dispatcher)
		s.InitialStateSent = true
	}

	stateChanged := false
	gameOver := s.Game.WinnerID != ""
	now := time.Now().Unix()

	// 2. CHECK GRACE PERIOD TIMEOUTS (15 seconds)
	if !gameOver {
		if s.Game.P1DisconnectTime > 0 && now-s.Game.P1DisconnectTime >= 15 {
			s.Game.WinnerID = s.Game.Player2ID // P1 timed out
			updateTrophies(ctx, logger, nk, s.Game.WinnerID, s.Game.Player1ID, s.Game.P2TimeUsed, s.Game.IsTimedMode)
			updateMatchStats(ctx, logger, nk, s.Game.WinnerID, s.Game.Player1ID)
			gameOver = true
			stateChanged = true
			logger.Info("Player 1 forfeited by timeout.")
		} else if s.Game.P2DisconnectTime > 0 && now-s.Game.P2DisconnectTime >= 15 {
			s.Game.WinnerID = s.Game.Player1ID // P2 timed out
			updateTrophies(ctx, logger, nk, s.Game.WinnerID, s.Game.Player2ID, s.Game.P1TimeUsed, s.Game.IsTimedMode)
			updateMatchStats(ctx, logger, nk, s.Game.WinnerID, s.Game.Player2ID)
			gameOver = true
			stateChanged = true
			logger.Info("Player 2 forfeited by timeout.")
		}
	}

	// 3. Process incoming messages (if game isn't over)
	if !gameOver {
		for _, msg := range messages {
			opCode := msg.GetOpCode()

			if opCode == 4 { // Ping
				dispatcher.BroadcastMessage(4, msg.GetData(), []runtime.Presence{msg}, nil, true)
				continue
			}

			if opCode == 1 { // Move
				var moveReq api.MoveRequest
				err := proto.Unmarshal(msg.GetData(), &moveReq)
				if err != nil {
					continue
				}

				// Reject moves if the other player is currently disconnected
				if s.Game.P1DisconnectTime > 0 || s.Game.P2DisconnectTime > 0 {
					continue
				}

				success := s.Game.AttemptMove(logger, msg.GetUserId(), moveReq.Position)
				if success {
					stateChanged = true
				}
			}
		}
	}

	// 4. Check normal win/draw
	if stateChanged && !gameOver {
		gameOver = s.Game.CheckWin()
		if !gameOver && s.Game.CheckDraw() {
			gameOver = true
		}

		if gameOver && s.Game.WinnerID != "" {
			loser := s.Game.Player1ID
			if s.Game.WinnerID == s.Game.Player1ID {
				loser = s.Game.Player2ID
			}
			updateTrophies(ctx, logger, nk, s.Game.WinnerID, loser, s.Game.P1TimeUsed, s.Game.IsTimedMode)
			updateMatchStats(ctx, logger, nk, s.Game.WinnerID, loser)
		}
	}

	// 5. Timer check (30 second turn forfeit in Timed Mode)
	if !gameOver && s.Game.IsTimedMode {
		// Only enforce turn timer if both players are connected!
		if s.Game.P1DisconnectTime == 0 && s.Game.P2DisconnectTime == 0 {
			if now-s.Game.TurnStartTime > 30 {
				var loser string
				if s.Game.CurrentTurn == 1 {
					s.Game.WinnerID = s.Game.Player2ID
					loser = s.Game.Player1ID
				} else {
					s.Game.WinnerID = s.Game.Player1ID
					loser = s.Game.Player2ID
				}
				updateTrophies(ctx, logger, nk, s.Game.WinnerID, loser, s.Game.P1TimeUsed, s.Game.IsTimedMode)
				updateMatchStats(ctx, logger, nk, s.Game.WinnerID, loser)
				stateChanged = true
				gameOver = true
			}
		}
	}

	// 6. Broadcast state
	if stateChanged {
		broadcastState(s, dispatcher)
		if gameOver {
			return nil // End the server match loop
		}
	}

	return s
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
	if !isTimedMode {
		if winnerTimeUsed <= 120 {
			trophiesToAward = 10
		} else if winnerTimeUsed <= 180 {
			trophiesToAward = 9
		} else if winnerTimeUsed <= 240 {
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
