package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"gamegateway/internal/identity"
)

type Store struct {
	pool *pgxpool.Pool
}

type Player struct {
	ID            string
	DisplayName   string
	Fingerprint   string
	NameConfirmed bool
}

type Game struct {
	ID            string
	Name          string
	Description   string
	EndpointURL   string
	Protocol      string
	MinCols       int
	MinRows       int
	MaxPlayers    int
	SupportsMouse bool
}

type ChatMessage struct {
	ID          int64
	RoomID      string
	PlayerID    string
	DisplayName string
	Body        string
	CreatedAt   time.Time
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schemaSQL); err != nil {
		return err
	}
	return s.migratePlayerNames(ctx)
}

func (s *Store) SeedSampleGame(ctx context.Context, endpointURL string) error {
	_, err := s.pool.Exec(ctx, `
		insert into games (id, name, description, endpoint_url, protocol, min_cols, min_rows, max_players, supports_mouse)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		on conflict (id) do update set
			name = excluded.name,
			description = excluded.description,
			endpoint_url = excluded.endpoint_url,
			protocol = excluded.protocol,
			min_cols = excluded.min_cols,
			min_rows = excluded.min_rows,
			max_players = excluded.max_players,
			supports_mouse = excluded.supports_mouse,
			updated_at = now()
	`, "cell-garden", "Meadow Village", "A tiny RPG-style village you can walk around with arrow keys or WASD.", endpointURL, "ggp.cell.v1", 60, 18, 1, false)
	return err
}

func (s *Store) EnsurePlayer(ctx context.Context, key identity.KeyInfo) (Player, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Player{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var player Player
	err = tx.QueryRow(ctx, `
		select p.id::text, p.display_name, k.fingerprint, p.name_confirmed
		from ssh_keys k
		join players p on p.id = k.player_id
		where k.fingerprint = $1 and k.revoked_at is null
	`, key.Fingerprint).Scan(&player.ID, &player.DisplayName, &player.Fingerprint, &player.NameConfirmed)

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Player{}, err
	}

	if errors.Is(err, pgx.ErrNoRows) {
		displayName, err := nextAvailableDisplayName(ctx, tx, cleanDisplayName(key.Username), "")
		if err != nil {
			return Player{}, err
		}

		err = tx.QueryRow(ctx, `
			insert into players (display_name, name_confirmed)
			values ($1, false)
			returning id::text, display_name, name_confirmed
		`, displayName).Scan(&player.ID, &player.DisplayName, &player.NameConfirmed)
		if err != nil {
			return Player{}, err
		}

		_, err = tx.Exec(ctx, `
			insert into ssh_keys (fingerprint, player_id, public_key, key_type)
			values ($1, $2, $3, $4)
		`, key.Fingerprint, player.ID, key.AuthorizedKey, key.KeyType)
		if err != nil {
			return Player{}, err
		}
		player.Fingerprint = key.Fingerprint
	}

	_, err = tx.Exec(ctx, `
		update players set last_seen_at = now() where id = $1
	`, player.ID)
	if err != nil {
		return Player{}, err
	}

	_, err = tx.Exec(ctx, `
		update ssh_keys set last_seen_at = now() where fingerprint = $1
	`, key.Fingerprint)
	if err != nil {
		return Player{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Player{}, err
	}
	return player, nil
}

func (s *Store) UpdatePlayerDisplayName(ctx context.Context, player Player, requested string) (Player, error) {
	requested = strings.TrimSpace(requested)
	if err := validateDisplayName(requested); err != nil {
		return Player{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Player{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	displayName, err := nextAvailableDisplayName(ctx, tx, requested, player.ID)
	if err != nil {
		return Player{}, err
	}

	err = tx.QueryRow(ctx, `
		update players
		set display_name = $1, name_confirmed = true, last_seen_at = now()
		where id = $2
		returning id::text, display_name, name_confirmed
	`, displayName, player.ID).Scan(&player.ID, &player.DisplayName, &player.NameConfirmed)
	if err != nil {
		return Player{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Player{}, err
	}
	return player, nil
}

func (s *Store) ListGames(ctx context.Context) ([]Game, error) {
	rows, err := s.pool.Query(ctx, `
		select id, name, description, endpoint_url, protocol, min_cols, min_rows, max_players, supports_mouse
		from games
		where enabled = true
		order by name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var games []Game
	for rows.Next() {
		var game Game
		if err := rows.Scan(&game.ID, &game.Name, &game.Description, &game.EndpointURL, &game.Protocol, &game.MinCols, &game.MinRows, &game.MaxPlayers, &game.SupportsMouse); err != nil {
			return nil, err
		}
		games = append(games, game)
	}
	return games, rows.Err()
}

func (s *Store) EnsureRoom(ctx context.Context, gameID string) (string, error) {
	roomID := fmt.Sprintf("%s:lobby", gameID)
	_, err := s.pool.Exec(ctx, `
		insert into rooms (id, game_id, name)
		values ($1, $2, $3)
		on conflict (id) do nothing
	`, roomID, gameID, "Lobby")
	return roomID, err
}

func (s *Store) RecentChat(ctx context.Context, roomID string, limit int) ([]ChatMessage, error) {
	rows, err := s.pool.Query(ctx, `
		select id, room_id, player_id::text, display_name, body, created_at
		from chat_messages
		where room_id = $1
		order by id desc
		limit $2
	`, roomID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reversed []ChatMessage
	for rows.Next() {
		var msg ChatMessage
		if err := rows.Scan(&msg.ID, &msg.RoomID, &msg.PlayerID, &msg.DisplayName, &msg.Body, &msg.CreatedAt); err != nil {
			return nil, err
		}
		reversed = append(reversed, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed, nil
}

func (s *Store) InsertChat(ctx context.Context, roomID string, player Player, body string) (ChatMessage, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return ChatMessage{}, errors.New("chat message is empty")
	}

	var msg ChatMessage
	err := s.pool.QueryRow(ctx, `
		insert into chat_messages (room_id, player_id, display_name, body)
		values ($1, $2, $3, $4)
		returning id, room_id, player_id::text, display_name, body, created_at
	`, roomID, player.ID, player.DisplayName, body).Scan(&msg.ID, &msg.RoomID, &msg.PlayerID, &msg.DisplayName, &msg.Body, &msg.CreatedAt)
	return msg, err
}

func cleanDisplayName(value string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if isASCIIAlphaNumeric(r) {
			b.WriteRune(r)
		}
	}
	cleaned := b.String()
	if cleaned == "" {
		cleaned = "traveler"
	}
	if len(cleaned) > 12 {
		return cleaned[:12]
	}
	return cleaned
}

func validateDisplayName(value string) error {
	if value == "" {
		return errors.New("name is required")
	}
	if len(value) > 12 {
		return errors.New("name must be 12 characters or fewer")
	}
	for _, r := range value {
		if !isASCIIAlphaNumeric(r) {
			return errors.New("name can only contain letters and numbers")
		}
	}
	return nil
}

func nextAvailableDisplayName(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, base string, excludePlayerID string) (string, error) {
	base = cleanDisplayName(base)
	for i := 0; i < 10000; i++ {
		candidate := candidateDisplayName(base, i)
		var exists bool
		err := q.QueryRow(ctx, `
			select exists(
				select 1
				from players
				where lower(display_name) = lower($1)
				and ($2 = '' or id::text <> $2)
			)
		`, candidate, excludePlayerID).Scan(&exists)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", errors.New("could not find an available name")
}

func candidateDisplayName(base string, attempt int) string {
	base = cleanDisplayName(base)
	if attempt == 0 {
		return base
	}
	suffix := strconv.Itoa(attempt)
	if len(suffix) >= 12 {
		return suffix[len(suffix)-12:]
	}
	prefixLen := 12 - len(suffix)
	if len(base) > prefixLen {
		base = base[:prefixLen]
	}
	return base + suffix
}

func isASCIIAlphaNumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func (s *Store) migratePlayerNames(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `
		alter table players add column if not exists name_confirmed boolean not null default false
	`); err != nil {
		return err
	}

	rows, err := s.pool.Query(ctx, `select id::text, display_name from players order by created_at, id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type existingPlayer struct {
		id   string
		name string
	}
	var players []existingPlayer
	for rows.Next() {
		var player existingPlayer
		if err := rows.Scan(&player.id, &player.name); err != nil {
			return err
		}
		players = append(players, player)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	used := make(map[string]struct{}, len(players))
	for _, player := range players {
		base := cleanDisplayName(player.name)
		name := ""
		for i := 0; i < 10000; i++ {
			candidate := candidateDisplayName(base, i)
			key := strings.ToLower(candidate)
			if _, ok := used[key]; ok {
				continue
			}
			used[key] = struct{}{}
			name = candidate
			break
		}
		if name == "" {
			return errors.New("could not normalize existing player names")
		}
		if _, err := s.pool.Exec(ctx, `update players set display_name = $1 where id = $2`, name, player.id); err != nil {
			return err
		}
	}

	if _, err := s.pool.Exec(ctx, `
		alter table players alter column display_name type varchar(12) using left(display_name, 12)
	`); err != nil {
		return err
	}

	var hasConstraint bool
	if err := s.pool.QueryRow(ctx, `
		select exists(select 1 from pg_constraint where conname = 'players_display_name_alnum_check')
	`).Scan(&hasConstraint); err != nil {
		return err
	}
	if !hasConstraint {
		if _, err := s.pool.Exec(ctx, `
			alter table players add constraint players_display_name_alnum_check check (display_name ~ '^[A-Za-z0-9]{1,12}$')
		`); err != nil {
			return err
		}
	}

	_, err = s.pool.Exec(ctx, `
		create unique index if not exists players_display_name_lower_idx on players (lower(display_name))
	`)
	return err
}

const schemaSQL = `
create table if not exists players (
  id uuid primary key default gen_random_uuid(),
  display_name varchar(12) not null,
  name_confirmed boolean not null default false,
  created_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now()
);

create table if not exists ssh_keys (
  fingerprint text primary key,
  player_id uuid not null references players(id) on delete cascade,
  public_key text not null,
  key_type text not null,
  first_seen_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  revoked_at timestamptz
);

create table if not exists games (
  id text primary key,
  name text not null,
  description text not null default '',
  endpoint_url text not null,
  protocol text not null default 'ggp.cell.v1',
  min_cols integer not null default 80,
  min_rows integer not null default 24,
  max_players integer not null default 1,
  supports_mouse boolean not null default false,
  enabled boolean not null default true,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists rooms (
  id text primary key,
  game_id text not null references games(id) on delete cascade,
  name text not null,
  created_at timestamptz not null default now()
);

create table if not exists chat_messages (
  id bigserial primary key,
  room_id text not null references rooms(id) on delete cascade,
  player_id uuid not null references players(id) on delete cascade,
  display_name text not null,
  body text not null,
  created_at timestamptz not null default now()
);

create index if not exists chat_messages_room_id_id_idx on chat_messages(room_id, id desc);
`
