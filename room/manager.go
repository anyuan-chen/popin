package room

import (
	"context"
	"sync"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type Manager struct {
	client *lksdk.RoomServiceClient
	rooms  sync.Map
}

func NewManager(url, apiKey, apiSecret string) *Manager {
	return &Manager{
		client: lksdk.NewRoomServiceClient(url, apiKey, apiSecret),
	}
}

func (m *Manager) CreateRoom(ctx context.Context, name string) (*livekit.Room, error) {
	return m.client.CreateRoom(ctx, &livekit.CreateRoomRequest{
		Name:            name,
		EmptyTimeout:    300,
		MaxParticipants: 10,
	})
}

func (m *Manager) GetRoom(ctx context.Context, name string) (*livekit.Room, error) {
	rooms, err := m.client.ListRooms(ctx, &livekit.ListRoomsRequest{
		Names: []string{name},
	})
	if err != nil {
		return nil, err
	}
	if len(rooms.Rooms) == 0 {
		return nil, nil
	}
	return rooms.Rooms[0], nil
}

func (m *Manager) ListRooms(ctx context.Context) ([]*livekit.Room, error) {
	rooms, err := m.client.ListRooms(ctx, &livekit.ListRoomsRequest{})
	if err != nil {
		return nil, err
	}
	return rooms.Rooms, nil
}

func (m *Manager) DeleteRoom(ctx context.Context, name string) error {
	_, err := m.client.DeleteRoom(ctx, &livekit.DeleteRoomRequest{
		Room: name,
	})
	return err
}

func (m *Manager) ListParticipants(ctx context.Context, roomName string) ([]*livekit.ParticipantInfo, error) {
	participants, err := m.client.ListParticipants(ctx, &livekit.ListParticipantsRequest{
		Room: roomName,
	})
	if err != nil {
		return nil, err
	}
	return participants.Participants, nil
}

func (m *Manager) RemoveParticipant(ctx context.Context, roomName, identity string) error {
	_, err := m.client.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{
		Room:     roomName,
		Identity: identity,
	})
	return err
}
