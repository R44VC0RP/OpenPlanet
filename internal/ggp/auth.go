package ggp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	jwtType      = "ggp-session+jwt"
	jwtAlgorithm = "HS256"
	minSecretLen = 32
)

type SessionTokenParams struct {
	Issuer    string
	Audience  string
	Player    Player
	SessionID string
	RoomID    string
	GameID    string
	Endpoint  string
	TTL       time.Duration
	Now       time.Time
	KeyID     string
}

type SessionTokenExpected struct {
	Issuer      string
	Audience    string
	Player      Player
	SessionID   string
	RoomID      string
	GameID      string
	Endpoint    string
	Now         time.Time
	Skew        time.Duration
	ReplayCache *ReplayCache
}

type SessionClaims struct {
	Issuer         string `json:"iss"`
	Audience       string `json:"aud"`
	Subject        string `json:"sub"`
	JWTID          string `json:"jti"`
	IssuedAt       int64  `json:"iat"`
	NotBefore      int64  `json:"nbf"`
	ExpiresAt      int64  `json:"exp"`
	Protocol       string `json:"ggp_protocol"`
	SessionID      string `json:"ggp_session_id"`
	RoomID         string `json:"ggp_room_id"`
	GameID         string `json:"ggp_game_id"`
	Endpoint       string `json:"ggp_endpoint,omitempty"`
	PlayerName     string `json:"ggp_player_name"`
	SSHFingerprint string `json:"ggp_ssh_fingerprint,omitempty"`
}

type jwtHeader struct {
	Type      string `json:"typ"`
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid,omitempty"`
}

type ReplayCache struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func NewReplayCache() *ReplayCache {
	return &ReplayCache{seen: make(map[string]time.Time)}
}

func NewSessionToken(secret string, params SessionTokenParams) (string, error) {
	if err := validateSecret(secret); err != nil {
		return "", err
	}
	if params.Issuer == "" || params.Audience == "" || params.Player.ID == "" || params.SessionID == "" || params.RoomID == "" || params.GameID == "" {
		return "", errors.New("session token params are incomplete")
	}
	if params.TTL <= 0 {
		params.TTL = 90 * time.Second
	}
	now := params.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	jti, err := randomTokenID()
	if err != nil {
		return "", err
	}

	header := jwtHeader{Type: jwtType, Algorithm: jwtAlgorithm, KeyID: params.KeyID}
	claims := SessionClaims{
		Issuer:         params.Issuer,
		Audience:       params.Audience,
		Subject:        params.Player.ID,
		JWTID:          jti,
		IssuedAt:       now.Unix(),
		NotBefore:      now.Add(-5 * time.Second).Unix(),
		ExpiresAt:      now.Add(params.TTL).Unix(),
		Protocol:       ProtocolCellV1,
		SessionID:      params.SessionID,
		RoomID:         params.RoomID,
		GameID:         params.GameID,
		Endpoint:       params.Endpoint,
		PlayerName:     params.Player.Name,
		SSHFingerprint: params.Player.SSHKeyFingerprint,
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := encodeSegment(headerJSON) + "." + encodeSegment(claimsJSON)
	return signingInput + "." + signSegment(secret, signingInput), nil
}

func ValidateSessionToken(secret, token string, expected SessionTokenExpected) (SessionClaims, error) {
	if err := validateSecret(secret); err != nil {
		return SessionClaims{}, err
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return SessionClaims{}, errors.New("session token must have three JWT segments")
	}
	signingInput := parts[0] + "." + parts[1]
	if subtle.ConstantTimeCompare([]byte(signSegment(secret, signingInput)), []byte(parts[2])) != 1 {
		return SessionClaims{}, errors.New("session token signature is invalid")
	}

	headerJSON, err := decodeSegment(parts[0])
	if err != nil {
		return SessionClaims{}, fmt.Errorf("decode session token header: %w", err)
	}
	var header jwtHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return SessionClaims{}, fmt.Errorf("parse session token header: %w", err)
	}
	if header.Type != jwtType || header.Algorithm != jwtAlgorithm {
		return SessionClaims{}, errors.New("session token header is not allowed")
	}

	claimsJSON, err := decodeSegment(parts[1])
	if err != nil {
		return SessionClaims{}, fmt.Errorf("decode session token claims: %w", err)
	}
	var claims SessionClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return SessionClaims{}, fmt.Errorf("parse session token claims: %w", err)
	}
	if err := validateClaims(claims, expected); err != nil {
		return SessionClaims{}, err
	}
	return claims, nil
}

func (c *ReplayCache) Accept(jti string, expiresAt time.Time, now time.Time) bool {
	if c == nil || jti == "" {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, exp := range c.seen {
		if !exp.After(now) {
			delete(c.seen, key)
		}
	}
	if _, ok := c.seen[jti]; ok {
		return false
	}
	c.seen[jti] = expiresAt
	return true
}

func validateClaims(claims SessionClaims, expected SessionTokenExpected) error {
	now := expected.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	skew := expected.Skew
	if skew <= 0 {
		skew = 10 * time.Second
	}
	if claims.Issuer == "" || claims.Audience == "" || claims.Subject == "" || claims.JWTID == "" {
		return errors.New("session token is missing registered claims")
	}
	if expected.Issuer != "" && claims.Issuer != expected.Issuer {
		return errors.New("session token issuer is invalid")
	}
	if expected.Audience != "" && claims.Audience != expected.Audience {
		return errors.New("session token audience is invalid")
	}
	if claims.Protocol != ProtocolCellV1 {
		return errors.New("session token protocol is invalid")
	}
	if expected.SessionID != "" && claims.SessionID != expected.SessionID {
		return errors.New("session token session binding is invalid")
	}
	if expected.RoomID != "" && claims.RoomID != expected.RoomID {
		return errors.New("session token room binding is invalid")
	}
	if expected.GameID != "" && claims.GameID != expected.GameID {
		return errors.New("session token game binding is invalid")
	}
	if expected.Endpoint != "" && claims.Endpoint != expected.Endpoint {
		return errors.New("session token endpoint binding is invalid")
	}
	if expected.Player.ID != "" && claims.Subject != expected.Player.ID {
		return errors.New("session token player binding is invalid")
	}
	if expected.Player.Name != "" && claims.PlayerName != expected.Player.Name {
		return errors.New("session token player name binding is invalid")
	}
	if expected.Player.SSHKeyFingerprint != "" && claims.SSHFingerprint != expected.Player.SSHKeyFingerprint {
		return errors.New("session token SSH key binding is invalid")
	}
	if now.Add(skew).Before(time.Unix(claims.NotBefore, 0)) {
		return errors.New("session token is not valid yet")
	}
	if !now.Add(-skew).Before(time.Unix(claims.ExpiresAt, 0)) {
		return errors.New("session token is expired")
	}
	if now.Add(5 * time.Minute).Before(time.Unix(claims.IssuedAt, 0)) {
		return errors.New("session token issued-at time is invalid")
	}
	if expected.ReplayCache != nil && !expected.ReplayCache.Accept(claims.JWTID, time.Unix(claims.ExpiresAt, 0), now) {
		return errors.New("session token was already used")
	}
	return nil
}

func validateSecret(secret string) error {
	if len(secret) < minSecretLen {
		return fmt.Errorf("GGP session secret must be at least %d bytes", minSecretLen)
	}
	return nil
}

func randomTokenID() (string, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func encodeSegment(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeSegment(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}

func signSegment(secret, value string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(value))
	return encodeSegment(mac.Sum(nil))
}
