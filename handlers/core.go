package handlers

import (
	"github.com/heroiclabs/nakama-common/runtime"
)

type GameHandlers struct {
	nk runtime.NakamaModule
}

func NewGameHandlers(nk runtime.NakamaModule) *GameHandlers {
	return &GameHandlers{
		nk: nk,
	}
}
