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
		if isTimed {
			logger.Info("Game Started between %s and %s in Timed Mode", p1, p2)
		} else {
			logger.Info("Game Started between %s and %s in Classic Mode", p1, p2)
		}
	}
	return s
}

func (m *MatchHandler) MatchLeave(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presences []runtime.Presence) interface{} {
	s := state.(*MatchState)

	// If the game hasn't started or is already over, just clean up quietly
	if s.Game == nil || s.Game.WinnerID != "" {
		for _, p := range presences {
			delete(s.Presences, p.GetUserId())
		}
		if len(s.Presences) == 0 {
			return nil
		}
		return s
	}

	// A player forfeited during an active game!
	for _, p := range presences {
		leaverID := p.GetUserId()
		delete(s.Presences, leaverID)

		// Figure out who the winner is (the person who DID NOT leave)
		var winnerID string
		switch leaverID {
		case s.Game.Player1ID:
			winnerID = s.Game.Player2ID
		case s.Game.Player2ID:
			winnerID = s.Game.Player1ID
		}

		// Award trophies and broadcast the final state
		if winnerID != "" {
			s.Game.WinnerID = winnerID

			// Award +10 to winner, -2 to leaver
			updateTrophies(ctx, logger, nk, winnerID, leaverID, s.Game.P1TimeUsed, s.Game.IsTimedMode)
			updateMatchStats(ctx, logger, nk, winnerID, leaverID)
			// Broadcast the game over state immediately
			protoState := &api.GameState{
				Board:         s.Game.Board,
				CurrentTurn:   s.Game.CurrentTurn,
				TurnStartTime: s.Game.TurnStartTime,
				WinnerId:      s.Game.WinnerID,
				P1Id:          s.Game.Player1ID,
				P2Id:          s.Game.Player2ID,
			}
			outBytes, _ := proto.Marshal(protoState)
			dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_STATE), outBytes, nil, nil, true)
		}
	}

	return nil
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
		protoState := &api.GameState{
			Board:         s.Game.Board,
			CurrentTurn:   s.Game.CurrentTurn,
			TurnStartTime: s.Game.TurnStartTime,
			P1Id:          s.Game.Player1ID,
			P2Id:          s.Game.Player2ID,
		}
		outBytes, _ := proto.Marshal(protoState)
		dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_STATE), outBytes, nil, nil, true)
		s.InitialStateSent = true
	}

	stateChanged := false
	gameOver := false

	// 2. Process incoming messages
	for _, msg := range messages {
		opCode := msg.GetOpCode()

		if opCode == 4 {
			dispatcher.BroadcastMessage(4, msg.GetData(), []runtime.Presence{msg}, nil, true)
			continue
		}

		if opCode == 1 {
			var moveReq api.MoveRequest
			err := proto.Unmarshal(msg.GetData(), &moveReq)
			if err != nil {
				continue // Skip processing if it's garbage data
			}

			if s.Game == nil {
				logger.Error("❌ FATAL: s.Game is nil when trying to move!")
				continue
			}

			success := s.Game.AttemptMove(logger, msg.GetUserId(), moveReq.Position)

			if success {
				stateChanged = true
			} else {
				logger.Warn("⚠️ Move rejected by game logic.")
			}
		}
	}

	// 3. Check win/draw
	if stateChanged {
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

	// 4. Timer check (30 second forfeit)
	if !gameOver && s.Game.IsTimedMode {
		now := time.Now().Unix()
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

	// 5. Broadcast state
	if stateChanged {
		protoState := &api.GameState{
			Board:         s.Game.Board,
			CurrentTurn:   s.Game.CurrentTurn,
			TurnStartTime: s.Game.TurnStartTime,
			WinnerId:      s.Game.WinnerID,
			P1Id:          s.Game.Player1ID,
			P2Id:          s.Game.Player2ID,
			IsTimedMode:   s.Game.IsTimedMode,
			P1TimeUsed:    s.Game.P1TimeUsed,
			P2TimeUsed:    s.Game.P2TimeUsed,
		}
		outBytes, _ := proto.Marshal(protoState)
		dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_STATE), outBytes, nil, nil, true)

		if gameOver {
			return nil
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
