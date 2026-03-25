package core

import (
	"time"

	runtime "github.com/heroiclabs/nakama-common/runtime" // Ensure this is imported
)

// Game holds the raw state of the match
type Game struct {
	Board         []int32
	CurrentTurn   int32 // 1 for Player X, 2 for Player O
	TurnStartTime int64
	WinnerID      string
	Player1ID     string
	Player2ID     string

	IsTimedMode bool
	P1TimeUsed  int64
	P2TimeUsed  int64
}

// NewGame initializes a fresh board
func NewGame(p1 string, p2 string, isTimed bool) *Game {
	return &Game{
		Board:         make([]int32, 9),
		CurrentTurn:   1,
		TurnStartTime: time.Now().Unix(),
		Player1ID:     p1,
		Player2ID:     p2,
		IsTimedMode:   isTimed,
	}
}

// AttemptMove validates and applies a move
func (g *Game) AttemptMove(logger runtime.Logger, playerID string, position int32) bool {
	// Validate bounds and empty cell
	logger.Info("Attempting move - PlayerID: %s, Position: %d, CurrentTurn: %d", playerID, position, g.CurrentTurn)
	if position < 0 || position > 8 || g.Board[position] != 0 {
		logger.Info("Invalid move - Position out of bounds or cell already occupied")
		return false
	}
	timeTaken := time.Now().Unix() - g.TurnStartTime
	if g.CurrentTurn == 1 {
		g.P1TimeUsed += timeTaken
	} else {
		g.P2TimeUsed += timeTaken
	}

	// Validate turn
	if (g.CurrentTurn == 1 && playerID != g.Player1ID) || (g.CurrentTurn == 2 && playerID != g.Player2ID) {
		logger.Info("Invalid move - Not current player's turn")
		return false
	}

	// Apply move
	g.Board[position] = g.CurrentTurn

	// Reset timer and swap turns
	g.TurnStartTime = time.Now().Unix()
	if g.CurrentTurn == 1 {
		g.CurrentTurn = 2
	} else {
		g.CurrentTurn = 1
	}

	return true
}

// CheckWin scans the board and updates the WinnerID if someone won
func (g *Game) CheckWin() bool {
	winningCombinations := [][]int32{
		{0, 1, 2}, {3, 4, 5}, {6, 7, 8}, // Rows
		{0, 3, 6}, {1, 4, 7}, {2, 5, 8}, // Cols
		{0, 4, 8}, {2, 4, 6}, // Diagonals
	}

	for _, combo := range winningCombinations {
		a, b, c := combo[0], combo[1], combo[2]
		if g.Board[a] != 0 && g.Board[a] == g.Board[b] && g.Board[a] == g.Board[c] {
			if g.Board[a] == 1 {
				g.WinnerID = g.Player1ID
			} else {
				g.WinnerID = g.Player2ID
			}
			return true
		}
	}
	return false
}

// CheckDraw returns true if there are no empty spaces left
func (g *Game) CheckDraw() bool {
	for _, cell := range g.Board {
		if cell == 0 {
			return false
		}
	}
	return true
}
