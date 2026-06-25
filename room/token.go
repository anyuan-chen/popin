package room

import (
	"time"

	"github.com/livekit/protocol/auth"
)

type TokenParams struct {
	RoomName       string
	Identity       string
	IsRecorder     bool
	CanPublish     bool
	CanSubscribe   bool
	CanPublishData bool
}

func (m *Manager) GenerateToken(apiKey, apiSecret string, params TokenParams) (string, error) {
	at := auth.NewAccessToken(apiKey, apiSecret)

	grant := &auth.VideoGrant{
		RoomJoin:       true,
		Room:           params.RoomName,
		CanPublish:     &params.CanPublish,
		CanSubscribe:   &params.CanSubscribe,
		CanPublishData: &params.CanPublishData,
		Recorder:       params.IsRecorder,
	}

	at.AddGrant(grant).
		SetIdentity(params.Identity).
		SetValidFor(24 * time.Hour)

	return at.ToJWT()
}
