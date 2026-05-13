package ggp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

type ProbeOptions struct {
	EndpointURL   string
	GameID        string
	Issuer        string
	SessionSecret string
	MaxPlayers    int
	AllowInsecure bool
	AllowPrivate  bool
}

type ProbeResult struct {
	Ready Ready
}

func ProbeEndpoint(ctx context.Context, opts ProbeOptions) (ProbeResult, error) {
	if err := ValidateEndpointURL(ctx, opts.EndpointURL, opts.AllowInsecure, opts.AllowPrivate); err != nil {
		return ProbeResult{}, err
	}
	if opts.GameID == "" {
		return ProbeResult{}, errors.New("game ID is required")
	}
	if opts.MaxPlayers > 1 && len(opts.SessionSecret) < minSecretLen {
		return ProbeResult{}, errors.New("multiplayer probe requires a 32+ byte game session secret")
	}

	sessionID := fmt.Sprintf("probe_%d", time.Now().UnixNano())
	roomID := opts.GameID + ":probe"
	player := Player{ID: "probe", Name: "Probe"}
	capabilities := []string{CapRenderCell, CapInputKeyboard, CapChatBridge}
	var auth *Auth
	if opts.SessionSecret != "" {
		token, err := NewSessionToken(opts.SessionSecret, SessionTokenParams{
			Issuer:    opts.Issuer,
			Audience:  opts.GameID,
			Player:    player,
			SessionID: sessionID,
			RoomID:    roomID,
			GameID:    opts.GameID,
			Endpoint:  opts.EndpointURL,
			TTL:       90 * time.Second,
		})
		if err != nil {
			return ProbeResult{}, err
		}
		capabilities = append(capabilities, CapAuthSession)
		auth = &Auth{Type: AuthTypeSessionJWT, Token: token}
	}

	client, err := Connect(ctx, opts.EndpointURL, Hello{
		Type:         TypeHello,
		Protocol:     ProtocolCellV1,
		SessionID:    sessionID,
		RoomID:       roomID,
		Player:       player,
		Viewport:     Viewport{Cols: 80, Rows: 24},
		Capabilities: capabilities,
		Auth:         auth,
	})
	if err != nil {
		return ProbeResult{}, err
	}
	defer client.Close()

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		switch {
		case ctx.Err() != nil:
			return ProbeResult{}, ctx.Err()
		default:
		}
		select {
		case event, ok := <-client.Events:
			if !ok {
				return ProbeResult{}, errors.New("game closed before ready")
			}
			if event.Error != nil {
				return ProbeResult{}, event.Error
			}
			if event.Ready == nil {
				continue
			}
			if !HasCapability(event.Ready.Capabilities, CapRenderCell) {
				return ProbeResult{}, errors.New("game did not advertise render.cell.v1")
			}
			if opts.MaxPlayers > 1 {
				if !HasCapability(event.Ready.Capabilities, CapAuthSession) || !HasCapability(event.Ready.Capabilities, CapMultiplayer) {
					return ProbeResult{}, errors.New("multiplayer games must advertise auth.session-token.v1 and multiplayer.room.v1")
				}
			}
			_ = client.SendResize(80, 24)
			return ProbeResult{Ready: *event.Ready}, nil
		case <-timer.C:
			return ProbeResult{}, errors.New("game did not send ready before timeout")
		}
	}
}

func ValidateEndpointURL(ctx context.Context, raw string, allowInsecure bool, allowPrivate bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}
	if parsed.Scheme != "wss" {
		if parsed.Scheme != "ws" || !allowInsecure {
			return errors.New("submitted endpoints must use wss://")
		}
	}
	host := parsed.Hostname()
	if host == "" {
		return errors.New("endpoint host is required")
	}
	if !allowPrivate {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return fmt.Errorf("resolve endpoint host: %w", err)
		}
		for _, addr := range ips {
			if isPrivateEndpointIP(addr.IP) {
				return errors.New("endpoint resolves to a private, loopback, or link-local address")
			}
		}
	}
	return nil
}

func HasCapability(capabilities []string, capability string) bool {
	for _, value := range capabilities {
		if value == capability {
			return true
		}
	}
	return false
}

func isPrivateEndpointIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
