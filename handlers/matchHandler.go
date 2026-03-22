package handlers

import (
	"context"
	"database/sql"
	"time"

	"tic-tac-toe/api"
	"tic-tac-toe/core"

	"github.com/heroiclabs/nakama-common/runtime"
	"google.golang.org/protobuf/proto"
)

// MatchHandler implements the Nakama Match interface
type MatchHandler struct{}

// MatchState holds the core game and presences (players) in the room
type MatchState struct {
	Game      *core.Game
	Presences map[string]runtime.Presence
}

func (m *MatchHandler) MatchInit(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, params map[string]interface{}) (interface{}, int, string) {
	state := &MatchState{
		Presences: make(map[string]runtime.Presence),
	}
	tickRate := 10 // 10 ticks per second
	label := "tictactoe"
	return state, tickRate, label
}

func (m *MatchHandler) MatchJoinAttempt(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presence runtime.Presence, metadata map[string]string) (interface{}, bool, string) {
	s := state.(*MatchState)
	// Only allow 2 players
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

	// Start the game if 2 players have joined
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

	for _, p := range presences {
		delete(s.Presences, p.GetUserId())

		if s.Game != nil && s.Game.WinnerID == "" {
			// Figure out who stayed
			loserID := p.GetUserId()
			if loserID == s.Game.Player1ID {
				s.Game.WinnerID = s.Game.Player2ID
			} else {
				s.Game.WinnerID = s.Game.Player1ID
			}

			// Broadcast Forfeit
			protoState := &api.GameState{
				Board:         s.Game.Board,
				CurrentTurn:   s.Game.CurrentTurn,
				TurnStartTime: s.Game.TurnStartTime,
				WinnerId:      s.Game.WinnerID,
			}
			outBytes, _ := proto.Marshal(protoState)
			dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_STATE), outBytes, nil, nil, true)

			// Update Trophies
			updateTrophies(ctx, logger, nk, s.Game.WinnerID, loserID)

			return nil
		}
	}

	if len(s.Presences) == 0 {
		return nil
	}
	return s
}
func (m *MatchHandler) MatchLoop(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, messages []runtime.MatchData) interface{} {
	s := state.(*MatchState)
	if s.Game == nil {
		return s
	}

	stateChanged := false
	gameOver := false
	isDraw := false

	// 1. TIMER CHECK: Enforce the 30-second forfeit rule
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

	// 2. Process incoming network messages
	for _, msg := range messages {
		// --- NEW: Custom Ping Echo for the frontend ---
		if msg.GetOpCode() == 4 {
			// Bounce the exact same payload back to the sender
			dispatcher.BroadcastMessage(4, msg.GetData(), []runtime.Presence{msg}, nil, true)
			continue
		}

		if msg.GetOpCode() == int64(api.OpCode_OPCODE_MOVE) {
			var moveReq api.MoveRequest
			if err := proto.Unmarshal(msg.GetData(), &moveReq); err == nil {
				if s.Game.AttemptMove(msg.GetUserId(), moveReq.Position) {
					stateChanged = true
				}
			}
		}
	}

	// 3. Check for Win or Draw (if we haven't already forfeited)
	if stateChanged && !gameOver {
		gameOver = s.Game.CheckWin()
		if !gameOver {
			isDraw = s.Game.CheckDraw()
			if isDraw {
				gameOver = true
			}
		} else {
			// Someone won via a legal move
			loser := s.Game.Player1ID
			if s.Game.WinnerID == s.Game.Player1ID {
				loser = s.Game.Player2ID
			}
			updateTrophies(ctx, logger, nk, s.Game.WinnerID, loser)
		}
	}

	// 4. Broadcast State and Handle End Game
	if stateChanged {
		protoState := &api.GameState{
			Board:         s.Game.Board,
			CurrentTurn:   s.Game.CurrentTurn,
			TurnStartTime: s.Game.TurnStartTime,
			WinnerId:      s.Game.WinnerID,
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
	// 1. Handle Winner (+10)
	var winnerScore int64 = 0
	// Fetch current score
	if records, _, _, _, err := nk.LeaderboardRecordsList(ctx, "global_trophies", []string{winnerID}, 1, "", 0); err == nil && len(records) > 0 {
		winnerScore = records[0].Score
	}
	// Write new score
	_, err := nk.LeaderboardRecordWrite(ctx, "global_trophies", winnerID, "", winnerScore+10, 0, nil, nil)
	if err != nil {
		logger.Error("Error writing winner trophies: %v", err)
	}

	// 2. Handle Loser (-2 if > 2)
	var loserScore int64 = 0
	if records, _, _, _, err := nk.LeaderboardRecordsList(ctx, "global_trophies", []string{loserID}, 1, "", 0); err == nil && len(records) > 0 {
		loserScore = records[0].Score
	}

	if loserScore > 2 {
		_, err = nk.LeaderboardRecordWrite(ctx, "global_trophies", loserID, "", loserScore-2, 0, nil, nil)
		if err != nil {
			logger.Error("Error writing loser trophies: %v", err)
		}
	}
}
