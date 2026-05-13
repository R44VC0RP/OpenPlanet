package ggp

import (
	"testing"
	"time"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestSessionTokenValidatesAndRejectsReplay(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	player := Player{ID: "player-1", Name: "Ryan", SSHKeyFingerprint: "SHA256:test"}
	token, err := NewSessionToken(testSecret, SessionTokenParams{
		Issuer:    "gateway",
		Audience:  "cell-garden",
		Player:    player,
		SessionID: "sess-1",
		RoomID:    "cell-garden:lobby",
		GameID:    "cell-garden",
		Endpoint:  "ws://sample-game:8081/ggp",
		Now:       now,
		TTL:       time.Minute,
	})
	if err != nil {
		t.Fatalf("NewSessionToken returned error: %v", err)
	}

	replay := NewReplayCache()
	expected := SessionTokenExpected{
		Issuer:      "gateway",
		Audience:    "cell-garden",
		Player:      player,
		SessionID:   "sess-1",
		RoomID:      "cell-garden:lobby",
		GameID:      "cell-garden",
		Endpoint:    "ws://sample-game:8081/ggp",
		Now:         now.Add(10 * time.Second),
		ReplayCache: replay,
	}
	if _, err := ValidateSessionToken(testSecret, token, expected); err != nil {
		t.Fatalf("ValidateSessionToken returned error: %v", err)
	}
	if _, err := ValidateSessionToken(testSecret, token, expected); err == nil {
		t.Fatal("ValidateSessionToken accepted replayed token")
	}
}

func TestSessionTokenRejectsWrongRoom(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	player := Player{ID: "player-1", Name: "Ryan"}
	token, err := NewSessionToken(testSecret, SessionTokenParams{
		Issuer:    "gateway",
		Audience:  "cell-garden",
		Player:    player,
		SessionID: "sess-1",
		RoomID:    "cell-garden:lobby",
		GameID:    "cell-garden",
		Now:       now,
		TTL:       time.Minute,
	})
	if err != nil {
		t.Fatalf("NewSessionToken returned error: %v", err)
	}

	_, err = ValidateSessionToken(testSecret, token, SessionTokenExpected{
		Issuer:    "gateway",
		Audience:  "cell-garden",
		Player:    player,
		SessionID: "sess-1",
		RoomID:    "cell-garden:other",
		GameID:    "cell-garden",
		Now:       now.Add(10 * time.Second),
	})
	if err == nil {
		t.Fatal("ValidateSessionToken accepted token bound to a different room")
	}
}
