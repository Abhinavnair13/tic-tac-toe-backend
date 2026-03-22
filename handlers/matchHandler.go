package handlers

import (
	"context"
	"database/sql"

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
	}
	// In a real game, you'd handle forfeit logic here
	return s
}

func (m *MatchHandler) MatchLoop(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, messages []runtime.MatchData) interface{} {
	s := state.(*MatchState)
	if s.Game == nil {
		return s // Waiting for players
	}

	stateChanged := false

	// Process incoming network messages
	for _, msg := range messages {
		if msg.GetOpCode() == int64(api.OpCode_OPCODE_MOVE) {
			var moveReq api.MoveRequest
			if err := proto.Unmarshal(msg.GetData(), &moveReq); err == nil {
				// Pass the data to our isolated core logic
				if s.Game.AttemptMove(msg.GetUserId(), moveReq.Position) {
					stateChanged = true
				}
			}
		}
	}

	// Check win condition
	if stateChanged {
		gameOver := s.Game.CheckWin()

		// Map the core state back to our Protobuf network payload
		protoState := &api.GameState{
			Board:         s.Game.Board,
			CurrentTurn:   s.Game.CurrentTurn,
			TurnStartTime: s.Game.TurnStartTime,
			WinnerId:      s.Game.WinnerID,
		}

		outBytes, _ := proto.Marshal(protoState)

		// Broadcast to clients
		dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_STATE), outBytes, nil, nil, true)

		if gameOver {
			logger.Info("Match won by %s", s.Game.WinnerID)
			return nil // Returning nil ends the match loop and destroys the goroutine
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
