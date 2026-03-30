package core

import (
	"time"

	"github.com/heroiclabs/nakama-common/runtime"
)

type Game struct {
	Board         []int32
	CurrentTurn   int32
	TurnStartTime int64
	WinnerID      string
	Player1ID     string
	Player2ID     string

	IsTimedMode bool
	P1TimeUsed  int64
	P2TimeUsed  int64

	P1DisconnectTime int64
	P2DisconnectTime int64
}

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

func (g *Game) Update(now int64) (bool, bool) {
	if g.WinnerID != "" {
		return false, true
	}

	if g.P1DisconnectTime > 0 && now-g.P1DisconnectTime >= 15 {
		g.WinnerID = g.Player2ID
		return true, true
	} else if g.P2DisconnectTime > 0 && now-g.P2DisconnectTime >= 15 {
		g.WinnerID = g.Player1ID
		return true, true
	}

	if g.IsTimedMode && g.P1DisconnectTime == 0 && g.P2DisconnectTime == 0 {
		if now-g.TurnStartTime > 30 {
			if g.CurrentTurn == 1 {
				g.WinnerID = g.Player2ID
			} else {
				g.WinnerID = g.Player1ID
			}
			return true, true
		}
	}

	return false, false
}

func (g *Game) AttemptMove(logger runtime.Logger, playerID string, position int32) bool {
	logger.Info("Attempting move - PlayerID: %s, Position: %d, CurrentTurn: %d", playerID, position, g.CurrentTurn)

	if g.P1DisconnectTime > 0 || g.P2DisconnectTime > 0 {
		logger.Info("Invalid move - Waiting for opponent to reconnect")
		return false
	}

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

	if (g.CurrentTurn == 1 && playerID != g.Player1ID) || (g.CurrentTurn == 2 && playerID != g.Player2ID) {
		logger.Info("Invalid move - Not current player's turn")
		return false
	}

	g.Board[position] = g.CurrentTurn
	g.TurnStartTime = time.Now().Unix()

	if g.CurrentTurn == 1 {
		g.CurrentTurn = 2
	} else {
		g.CurrentTurn = 1
	}

	return true
}

func (g *Game) PlayerDisconnected(playerID string, now int64) {
	if playerID == g.Player1ID {
		g.P1DisconnectTime = now
	} else if playerID == g.Player2ID {
		g.P2DisconnectTime = now
	}
}

func (g *Game) PlayerReconnected(playerID string) {
	if playerID == g.Player1ID {
		g.P1DisconnectTime = 0
	} else if playerID == g.Player2ID {
		g.P2DisconnectTime = 0
	}
}

func (g *Game) CheckWin() bool {
	winningCombinations := [][]int32{
		{0, 1, 2}, {3, 4, 5}, {6, 7, 8},
		{0, 3, 6}, {1, 4, 7}, {2, 5, 8},
		{0, 4, 8}, {2, 4, 6},
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

func (g *Game) CheckDraw() bool {
	for _, cell := range g.Board {
		if cell == 0 {
			return false
		}
	}
	return true
}

func (g *Game) GetLoserID() string {
	if g.WinnerID == g.Player1ID {
		return g.Player2ID
	} else if g.WinnerID == g.Player2ID {
		return g.Player1ID
	}
	return ""
}

func (g *Game) GetWinnerTimeUsed() int64 {
	if g.WinnerID == g.Player1ID {
		return g.P1TimeUsed
	}
	return g.P2TimeUsed
}
