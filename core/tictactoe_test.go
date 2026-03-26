package core

import (
	"testing"
	"time"

	"github.com/heroiclabs/nakama-common/runtime"
)

// ==========================================
// MOCK LOGGER (Required so AttemptMove doesn't panic)
// ==========================================
type MockLogger struct{}

func (l *MockLogger) Debug(format string, v ...interface{})                   {}
func (l *MockLogger) Info(format string, v ...interface{})                    {}
func (l *MockLogger) Warn(format string, v ...interface{})                    {}
func (l *MockLogger) Error(format string, v ...interface{})                   {}
func (l *MockLogger) WithField(key string, v interface{}) runtime.Logger      { return l }
func (l *MockLogger) WithFields(fields map[string]interface{}) runtime.Logger { return l }
func (l *MockLogger) Fields() map[string]interface{}                          { return nil }

var logger = &MockLogger{}

// ==========================================
// TESTS
// ==========================================

func TestNewGame(t *testing.T) {
	game := NewGame("p1", "p2", true)

	if game.Player1ID != "p1" || game.Player2ID != "p2" {
		t.Errorf("Players not assigned correctly")
	}
	if game.CurrentTurn != 1 {
		t.Errorf("Game should start on Player 1's turn")
	}
	if len(game.Board) != 9 {
		t.Errorf("Board should have 9 spaces")
	}
	if !game.IsTimedMode {
		t.Errorf("IsTimedMode should be true")
	}
}

func TestValidMoveAndTurnSwap(t *testing.T) {
	game := NewGame("p1", "p2", true)

	// Simulate a tiny delay so TurnStartTime logic actually triggers > 0
	game.TurnStartTime = time.Now().Unix() - 2

	success := game.AttemptMove(logger, "p1", 4) // Center square

	if !success {
		t.Errorf("Valid move was rejected")
	}
	if game.Board[4] != 1 {
		t.Errorf("Board was not updated with Player 1's mark")
	}
	if game.CurrentTurn != 2 {
		t.Errorf("Turn did not swap to Player 2")
	}
	if game.P1TimeUsed < 2 {
		t.Errorf("P1TimeUsed was not incremented correctly. Got: %d", game.P1TimeUsed)
	}
}

func TestInvalidMoves_OutOfBounds(t *testing.T) {
	game := NewGame("p1", "p2", false)

	if game.AttemptMove(logger, "p1", -1) {
		t.Errorf("Move allowed at negative index")
	}
	if game.AttemptMove(logger, "p1", 9) {
		t.Errorf("Move allowed beyond board size")
	}
}

func TestInvalidMoves_WrongPlayerTurn(t *testing.T) {
	game := NewGame("p1", "p2", false)

	// P2 tries to move on P1's turn
	if game.AttemptMove(logger, "p2", 0) {
		t.Errorf("Player 2 was allowed to move on Player 1's turn")
	}

	// P1 makes a valid move
	game.AttemptMove(logger, "p1", 0)

	// P1 tries to move AGAIN on P2's turn
	if game.AttemptMove(logger, "p1", 1) {
		t.Errorf("Player 1 was allowed to move twice in a row")
	}
}

func TestInvalidMoves_CellOccupied(t *testing.T) {
	game := NewGame("p1", "p2", false)

	game.AttemptMove(logger, "p1", 0) // P1 takes Top-Left

	if game.AttemptMove(logger, "p2", 0) { // P2 tries to take Top-Left
		t.Errorf("Player was allowed to overwrite an occupied cell")
	}
}

func TestWinConditions(t *testing.T) {
	// Table-driven tests for all win directions
	tests := []struct {
		name     string
		board    []int32
		winnerID string // "p1" (1) or "p2" (2)
	}{
		{"Row 1 Win P1", []int32{1, 1, 1, 0, 0, 0, 0, 0, 0}, "p1"},
		{"Row 2 Win P2", []int32{1, 0, 0, 2, 2, 2, 1, 0, 0}, "p2"},
		{"Col 1 Win P1", []int32{1, 2, 0, 1, 2, 0, 1, 0, 0}, "p1"},
		{"Col 3 Win P2", []int32{1, 0, 2, 1, 0, 2, 0, 0, 2}, "p2"},
		{"Diag 1 Win P1", []int32{1, 2, 0, 0, 1, 0, 0, 2, 1}, "p1"},
		{"Diag 2 Win P2", []int32{1, 0, 2, 1, 2, 0, 2, 0, 1}, "p2"},
		{"No Win Yet", []int32{1, 2, 1, 0, 0, 0, 0, 0, 0}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			game := NewGame("p1", "p2", false)
			game.Board = tt.board

			won := game.CheckWin()

			if tt.winnerID == "" && won {
				t.Errorf("CheckWin returned true for an incomplete game")
			} else if tt.winnerID != "" && !won {
				t.Errorf("CheckWin failed to detect a win")
			}

			if won && game.WinnerID != tt.winnerID {
				t.Errorf("Expected winner %s, but got %s", tt.winnerID, game.WinnerID)
			}
		})
	}
}

func TestDrawCondition(t *testing.T) {
	game := NewGame("p1", "p2", false)

	// Setup a classic Cat's Game (Draw) board
	// X O X
	// O O X
	// X X O
	game.Board = []int32{
		1, 2, 1,
		2, 2, 1,
		1, 1, 2,
	}

	if game.CheckWin() {
		t.Errorf("CheckWin falsely detected a win on a drawn board")
	}

	if !game.CheckDraw() {
		t.Errorf("CheckDraw failed to detect the full, drawn board")
	}
}

func TestNotDrawIfGameWonOnLastMove(t *testing.T) {
	game := NewGame("p1", "p2", false)

	// Setup a board that is full, BUT X won on the very last placement
	// X O X
	// O X O
	// O X X
	game.Board = []int32{
		1, 2, 1,
		2, 1, 2,
		2, 1, 1,
	}

	if !game.CheckWin() {
		t.Errorf("CheckWin failed to detect the diagonal win")
	}

	// NOTE: In MatchLoop logic, you check CheckWin() FIRST.
	// CheckDraw() is only evaluated if CheckWin is false, but we can verify CheckDraw's raw output.
	if !game.CheckDraw() {
		t.Errorf("CheckDraw should technically return true here because the board is physically full")
	}
}
