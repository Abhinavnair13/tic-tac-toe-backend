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
	p1, p2 := "user1", "user2"
	game := NewGame(p1, p2, true)

	if game.Player1ID != p1 || game.Player2ID != p2 {
		t.Errorf("Expected IDs %s and %s, got %s and %s", p1, p2, game.Player1ID, game.Player2ID)
	}
	if game.CurrentTurn != 1 {
		t.Errorf("Expected turn 1, got %d", game.CurrentTurn)
	}
	if !game.IsTimedMode {
		t.Error("Expected timed mode to be true")
	}
}

func TestAttemptMove_Valid(t *testing.T) {
	game := NewGame("p1", "p2", false)

	// Player 1 moves to center
	success := game.AttemptMove(logger, "p1", 4)
	if !success {
		t.Fatal("Valid move for P1 failed")
	}
	if game.Board[4] != 1 {
		t.Errorf("Expected board[4] to be 1, got %d", game.Board[4])
	}
	if game.CurrentTurn != 2 {
		t.Error("Turn did not switch to Player 2")
	}
}

func TestAttemptMove_Invalid(t *testing.T) {
	game := NewGame("p1", "p2", false)

	// 1. Wrong player's turn
	if game.AttemptMove(logger, "p2", 0) {
		t.Error("P2 was allowed to move during P1's turn")
	}

	// 2. Out of bounds
	if game.AttemptMove(logger, "p1", 10) {
		t.Error("Move allowed out of bounds")
	}

	// 3. Occupied cell
	game.AttemptMove(logger, "p1", 0)
	if game.AttemptMove(logger, "p2", 0) {
		t.Error("Move allowed on already occupied cell")
	}

	// 4. Move while disconnected
	game.PlayerDisconnected("p2", time.Now().Unix())
	if game.AttemptMove(logger, "p1", 1) {
		t.Error("Move allowed while a player is disconnected")
	}
}

func TestCheckWin(t *testing.T) {
	tests := []struct {
		name     string
		board    []int32
		winnerID string
	}{
		{"Row Win", []int32{1, 1, 1, 0, 2, 2, 0, 0, 0}, "p1"},
		{"Col Win", []int32{2, 0, 0, 2, 1, 0, 2, 1, 1}, "p2"},
		{"Diag Win", []int32{1, 0, 2, 0, 1, 2, 0, 0, 1}, "p1"},
		{"No Win", []int32{1, 2, 1, 0, 0, 0, 0, 0, 0}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			game := NewGame("p1", "p2", false)
			game.Board = tt.board
			won := game.CheckWin()
			if tt.winnerID != "" && !won {
				t.Error("Expected a win, but CheckWin returned false")
			}
			if game.WinnerID != tt.winnerID {
				t.Errorf("Expected winner %s, got %s", tt.winnerID, game.WinnerID)
			}
		})
	}
}

func TestCheckDraw(t *testing.T) {
	game := NewGame("p1", "p2", false)
	// Full board with no winner
	game.Board = []int32{
		1, 2, 1,
		1, 2, 2,
		2, 1, 1,
	}
	if !game.CheckDraw() {
		t.Error("Expected Draw to be true")
	}
}

func TestUpdate_DisconnectTimeout(t *testing.T) {
	game := NewGame("p1", "p2", false)
	now := time.Now().Unix()

	// P1 disconnects
	game.PlayerDisconnected("p1", now)

	// Check 10 seconds later (no timeout)
	changed, over := game.Update(now + 10)
	if changed || over {
		t.Error("Game timed out too early")
	}

	// Check 16 seconds later (timeout)
	changed, over = game.Update(now + 16)
	if !changed || !over {
		t.Error("Game failed to timeout after 15s")
	}
	if game.WinnerID != "p2" {
		t.Errorf("P2 should have won via timeout, got %s", game.WinnerID)
	}
}

func TestUpdate_TurnTimeout(t *testing.T) {
	game := NewGame("p1", "p2", true) // Timed Mode
	start := game.TurnStartTime

	// 1. Wait 31 seconds (Normal turn timeout)
	changed, over := game.Update(start + 31)
	if !changed || !over {
		t.Error("Turn timer failed to trigger after 30s")
	}
	if game.WinnerID != "p2" {
		t.Errorf("P1 timed out, P2 should win. Got: %s", game.WinnerID)
	}

	// 2. Ensure Turn Timer DOES NOT trigger if someone is disconnected
	game2 := NewGame("p1", "p2", true)

	// Force the turn to have started 35 seconds ago (Turn Timer Expired)
	game2.TurnStartTime = start - 35
	// But the player ONLY JUST disconnected (Grace Period Active)
	game2.PlayerDisconnected("p2", start)

	// Check state 5 seconds after disconnect
	changed, over = game2.Update(start + 5)

	if changed || over {
		t.Error("Turn timer triggered while a player was in their disconnect grace period")
	}
}

func TestTimeUsedTracking(t *testing.T) {
	game := NewGame("p1", "p2", false)

	// Fake the turn start to 10 seconds ago
	game.TurnStartTime = time.Now().Unix() - 10
	game.AttemptMove(logger, "p1", 0)

	if game.P1TimeUsed < 10 {
		t.Errorf("Expected P1TimeUsed to be at least 10, got %d", game.P1TimeUsed)
	}

	// THE FIX: Declare P1 as the winner before asking for the winner's time!
	game.WinnerID = "p1"

	if game.GetWinnerTimeUsed() != game.P1TimeUsed {
		t.Error("GetWinnerTimeUsed did not return P1's time when P1 is the winner")
	}
}
