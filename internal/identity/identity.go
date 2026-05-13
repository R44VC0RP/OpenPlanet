package identity

import (
	"errors"
	"strings"

	"github.com/charmbracelet/ssh"
	gossh "golang.org/x/crypto/ssh"
)

type KeyInfo struct {
	Username      string
	Fingerprint   string
	AuthorizedKey string
	KeyType       string
}

func FromSession(s ssh.Session) (KeyInfo, error) {
	key := s.PublicKey()
	if key == nil {
		return KeyInfo{}, errors.New("session did not present a public key")
	}

	authorizedKey := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(key)))
	fields := strings.Fields(authorizedKey)
	keyType := "unknown"
	if len(fields) > 0 {
		keyType = fields[0]
	}

	username := strings.TrimSpace(s.User())
	if username == "" {
		username = "traveler"
	}

	return KeyInfo{
		Username:      username,
		Fingerprint:   gossh.FingerprintSHA256(key),
		AuthorizedKey: authorizedKey,
		KeyType:       keyType,
	}, nil
}
