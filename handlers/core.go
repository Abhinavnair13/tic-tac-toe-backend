package handlers

import (
	"github.com/heroiclabs/nakama-common/runtime"
)

// GameHandlers acts as the central hub for your custom game logic.
type GameHandlers struct {
	nk runtime.NakamaModule
}

// NewGameHandlers only takes the persistent dependencies
func NewGameHandlers(nk runtime.NakamaModule) *GameHandlers {
	return &GameHandlers{
		nk: nk,
	}
}
