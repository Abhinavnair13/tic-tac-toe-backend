package handlers

import (
	"context"
	"database/sql"
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
}

func (m *MatchHandler) MatchInit(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, params map[string]interface{}) (interface{}, int, string) {
	state := &MatchState{
		Presences: make(map[string]runtime.Presence),
	}
	tickRate := 10
	label := "tictactoe"
	return state, tickRate, label
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
		s.Game = core.NewGame(p1, p2)
		logger.Info("Game Started between %s and %s", p1, p2)
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
			updateTrophies(ctx, logger, nk, winnerID, leaverID)

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
			updateTrophies(ctx, logger, nk, s.Game.WinnerID, loser)
		}
	}

	// 4. Timer check (30 second forfeit)
	if !gameOver {
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
			updateTrophies(ctx, logger, nk, s.Game.WinnerID, loser)
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

func updateTrophies(ctx context.Context, logger runtime.Logger, nk runtime.NakamaModule, winnerID string, loserID string) {
	// 1. UPDATE WINNER (+10)
	var winnerScore int64 = 0

	// FIX: Use ownerRecords (the 2nd return value) to get this specific player's score
	_, ownerRecords, _, _, err := nk.LeaderboardRecordsList(ctx, "global_trophies", []string{winnerID}, 1, "", 0)
	if err == nil && len(ownerRecords) > 0 {
		winnerScore = ownerRecords[0].Score
	}
	logger.Warn("Winner score: %d", winnerScore)

	_, err = nk.LeaderboardRecordWrite(ctx, "global_trophies", winnerID, "", winnerScore+10, 0, nil, nil)
	if err != nil {
		logger.Error("Error writing winner trophies: %v", err)
	}

	// 2. UPDATE LOSER (-2)
	var loserScore int64 = 0

	// FIX: Use ownerRecords here as well
	_, loserOwnerRecords, _, _, err := nk.LeaderboardRecordsList(ctx, "global_trophies", []string{loserID}, 1, "", 0)
	if err == nil && len(loserOwnerRecords) > 0 {
		loserScore = loserOwnerRecords[0].Score
	}
	logger.Warn("Loser score: %d", loserScore)

	// Only apply penalty if they have trophies to lose
	if loserScore > 2 {
		newLoserScore := loserScore - 2

		// Prevent trophies from dropping below 0
		if newLoserScore < 0 {
			newLoserScore = 0
		}

		_, err = nk.LeaderboardRecordWrite(ctx, "global_trophies", loserID, "", newLoserScore, 0, nil, nil)
		if err != nil {
			logger.Error("Error writing loser trophies: %v", err)
		}
	}
}
