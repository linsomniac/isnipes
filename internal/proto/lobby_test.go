package proto

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestLobbyEnvelopeRoundTrip(t *testing.T) {
	in := Hello{Nick: "Alice", ClientVersion: "v0.0.1", SchemaChecksum: 0xDEADBEEF}
	b, err := EncodeLobbyMessage(LobbyHello, in)
	if err != nil {
		t.Fatal(err)
	}
	env, err := DecodeLobbyEnvelope(b)
	if err != nil {
		t.Fatal(err)
	}
	if env.T != LobbyHello || env.V != LobbyEnvelopeVersion {
		t.Fatalf("envelope: %+v", env)
	}
	var got Hello
	if err := json.Unmarshal(env.D, &got); err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Fatalf("payload mismatch: %+v vs %+v", in, got)
	}
}

func TestLobbyEnvelopeRejectsWrongVersion(t *testing.T) {
	b, _ := json.Marshal(Envelope{T: "hello", V: 2, D: json.RawMessage(`{}`)})
	if _, err := DecodeLobbyEnvelope(b); !errors.Is(err, ErrLobbyVersion) {
		t.Fatalf("err = %v, want ErrLobbyVersion", err)
	}
}

func TestLobbyEncodeAllTypes(t *testing.T) {
	cases := []struct {
		t string
		p any
	}{
		{LobbyHello, Hello{Nick: "x", SchemaChecksum: 1}},
		{LobbyWelcome, Welcome{PlayerID: "p", Nick: "x", SchemaChecksum: 1}},
		{LobbyRoomList, RoomList{Rooms: []RoomDescriptor{{ID: "ABC", Name: "n", Players: 1, Max: 8, Mode: "ffa", Level: Level{"A", 1}, State: "OPEN"}}}},
		{LobbyCreateRoom, CreateRoom{Name: "n", Max: 4, Level: Level{"A", 1}}},
		{LobbyJoinRoom, JoinRoom{RoomID: "ABC"}},
		{LobbyLeaveRoom, LeaveRoom{}},
		{LobbyStartMatch, StartMatch{RoomID: "ABC"}},
		{LobbyMatchStarted, MatchStarted{MatchID: "M", GameSocketPath: "/ws/match/M", TickRate: 30, MapSeed: 1, JoinToken: "t"}},
		{LobbyTagError, LobbyError{Code: LobbyErrVersion, Message: "msg"}},
	}
	for _, c := range cases {
		b, err := EncodeLobbyMessage(c.t, c.p)
		if err != nil {
			t.Errorf("%s: encode: %v", c.t, err)
			continue
		}
		env, err := DecodeLobbyEnvelope(b)
		if err != nil {
			t.Errorf("%s: decode: %v", c.t, err)
			continue
		}
		if env.T != c.t {
			t.Errorf("%s: got tag %s", c.t, env.T)
		}
	}
}
