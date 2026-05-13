package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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

const (
	RoleStandard = "standard"
	RoleAdmin    = "admin"

	GameStatusPending  = "pending"
	GameStatusApproved = "approved"
	GameStatusRejected = "rejected"
	GameStatusDisabled = "disabled"

	CheckStatusPassed = "passed"
	CheckStatusFailed = "failed"
)

var (
	gameIDPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	imageRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{2,255}$`)
)

type Player struct {
	ID            string
	DisplayName   string
	Fingerprint   string
	NameConfirmed bool
	Role          string
}

type Game struct {
	ID              string
	Name            string
	Description     string
	EndpointURL     string
	Protocol        string
	MinCols         int
	MinRows         int
	MaxPlayers      int
	SupportsMouse   bool
	Status          string
	SubmittedBy     string
	SubmittedByName string
	ReviewedBy      string
	ReviewNote      string
	LastCheckStatus string
	LastCheckError  string
	SessionSecret   string
	ImageRef        string
	ImageDigest     string
	ContainerPort   int
	RuntimeStatus   string
	RuntimeError    string
	SubmittedAt     time.Time
	ReviewedAt      time.Time
	LastCheckedAt   time.Time
}

type GameSubmission struct {
	ID              string
	Name            string
	Description     string
	ImageRef        string
	ContainerPort   int
	MinCols         int
	MinRows         int
	MaxPlayers      int
	SupportsMouse   bool
	SubmittedBy     string
	SessionSecret   string
	LastCheckStatus string
	LastCheckError  string
}

type ImageGame struct {
	ID            string
	ImageRef      string
	ContainerPort int
	SessionSecret string
	Status        string
}

type scanner interface {
	Scan(dest ...any) error
}

type ChatMessage struct {
	ID          int64
	RoomID      string
	PlayerID    string
	DisplayName string
	Body        string
	CreatedAt   time.Time
}

type LeaderboardEntry struct {
	GameID      string
	PlayerID    string
	DisplayName string
	Score       int64
	UpdatedAt   time.Time
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
	if err := s.migratePlayerNames(ctx); err != nil {
		return err
	}
	return s.migrateModeration(ctx)
}

func (s *Store) SeedSampleGame(ctx context.Context, endpointURL string, maxPlayers int, sessionSecret string) error {
	if maxPlayers < 1 {
		maxPlayers = 1
	}
	_, err := s.pool.Exec(ctx, `
		insert into games (id, name, description, endpoint_url, protocol, min_cols, min_rows, max_players, supports_mouse, status, enabled, session_secret, image_ref, container_port, last_check_status, last_check_error, last_checked_at, runtime_status, runtime_error)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'approved', true, $10, '', 8081, 'passed', '', now(), 'running', '')
		on conflict (id) do update set
			name = excluded.name,
			description = excluded.description,
			endpoint_url = excluded.endpoint_url,
			protocol = excluded.protocol,
			min_cols = excluded.min_cols,
			min_rows = excluded.min_rows,
			max_players = excluded.max_players,
			supports_mouse = excluded.supports_mouse,
			status = 'approved',
			enabled = true,
			session_secret = excluded.session_secret,
			image_ref = '',
			container_port = 8081,
			last_check_status = 'passed',
			last_check_error = '',
			last_checked_at = now(),
			runtime_status = 'running',
			runtime_error = '',
			updated_at = now()
	`, "cell-garden", "Meadow Quest", "A multiplayer terminal RPG with monsters, loot, house exploration, potions, and score chasing.", endpointURL, "ggp.cell.v1", 60, 18, maxPlayers, false, sessionSecret)
	return err
}

func (s *Store) SeedBlobfieldGame(ctx context.Context, endpointURL string, maxPlayers int, sessionSecret string) error {
	if maxPlayers < 1 {
		maxPlayers = 1
	}
	_, err := s.pool.Exec(ctx, `
		insert into games (id, name, description, endpoint_url, protocol, min_cols, min_rows, max_players, supports_mouse, status, enabled, session_secret, image_ref, container_port, last_check_status, last_check_error, last_checked_at, runtime_status, runtime_error)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'approved', true, $10, '', 8082, 'passed', '', now(), 'running', '')
		on conflict (id) do update set
			name = excluded.name,
			description = excluded.description,
			endpoint_url = excluded.endpoint_url,
			protocol = excluded.protocol,
			min_cols = excluded.min_cols,
			min_rows = excluded.min_rows,
			max_players = excluded.max_players,
			supports_mouse = excluded.supports_mouse,
			status = 'approved',
			enabled = true,
			session_secret = excluded.session_secret,
			image_ref = '',
			container_port = 8082,
			last_check_status = 'passed',
			last_check_error = '',
			last_checked_at = now(),
			runtime_status = 'running',
			runtime_error = '',
			updated_at = now()
	`, "blobfield", "Blobfield", "A multiplayer blob arena: eat pellets, grow big, and swallow smaller players.", endpointURL, "ggp.cell.v1", 70, 20, maxPlayers, false, sessionSecret)
	return err
}

func (s *Store) SeedTetrisGame(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		insert into games (id, name, description, endpoint_url, protocol, min_cols, min_rows, max_players, supports_mouse, status, enabled, session_secret, image_ref, container_port, last_check_status, last_check_error, last_checked_at, runtime_status, runtime_error)
		values ('tetris', 'Tetris', 'Classic falling-block puzzle game.', 'ws://gamegateway-game-tetris:8080/ggp', 'ggp.cell.v1', 40, 20, 1, false, 'approved', true, '', 'ghcr.io/0ximjosh/tetris-server:latest', 8080, 'passed', '', now(), 'running', '')
		on conflict (id) do update set
			name = excluded.name,
			description = excluded.description,
			endpoint_url = excluded.endpoint_url,
			protocol = excluded.protocol,
			min_cols = excluded.min_cols,
			min_rows = excluded.min_rows,
			max_players = excluded.max_players,
			supports_mouse = excluded.supports_mouse,
			status = 'approved',
			enabled = true,
			image_ref = excluded.image_ref,
			container_port = excluded.container_port,
			last_check_status = 'passed',
			last_check_error = '',
			last_checked_at = now(),
			runtime_status = 'running',
			runtime_error = '',
			updated_at = now()
	`)
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
		select p.id::text, p.display_name, k.fingerprint, p.name_confirmed, p.role
		from ssh_keys k
		join players p on p.id = k.player_id
		where k.fingerprint = $1 and k.revoked_at is null
	`, key.Fingerprint).Scan(&player.ID, &player.DisplayName, &player.Fingerprint, &player.NameConfirmed, &player.Role)

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Player{}, err
	}

	if errors.Is(err, pgx.ErrNoRows) {
		displayName, err := nextAvailableDisplayName(ctx, tx, cleanDisplayName(key.Username), "")
		if err != nil {
			return Player{}, err
		}

		err = tx.QueryRow(ctx, `
			insert into players (display_name, name_confirmed, role)
			values ($1, false, 'standard')
			returning id::text, display_name, name_confirmed, role
		`, displayName).Scan(&player.ID, &player.DisplayName, &player.NameConfirmed, &player.Role)
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
		returning id::text, display_name, name_confirmed, role
	`, displayName, player.ID).Scan(&player.ID, &player.DisplayName, &player.NameConfirmed, &player.Role)
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
		select games.id, games.name, games.description, games.endpoint_url, games.protocol, games.min_cols, games.min_rows, games.max_players, games.supports_mouse,
			status, coalesce(submitted_by::text, ''), coalesce(submitter.display_name, ''), coalesce(reviewed_by::text, ''), coalesce(review_note, ''),
			coalesce(last_check_status, ''), coalesce(last_check_error, ''), coalesce(session_secret, ''), coalesce(image_ref, ''), coalesce(image_digest, ''), container_port, coalesce(runtime_status, ''), coalesce(runtime_error, ''),
			submitted_at, coalesce(reviewed_at, '0001-01-01'::timestamptz), coalesce(last_checked_at, '0001-01-01'::timestamptz)
		from games
		left join players submitter on submitter.id = games.submitted_by
		where enabled = true and status = 'approved'
		order by name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var games []Game
	for rows.Next() {
		game, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, game)
	}
	return games, rows.Err()
}

func (s *Store) ListSubmittedGames(ctx context.Context) ([]Game, error) {
	rows, err := s.pool.Query(ctx, `
		select games.id, games.name, games.description, games.endpoint_url, games.protocol, games.min_cols, games.min_rows, games.max_players, games.supports_mouse,
			status, coalesce(submitted_by::text, ''), coalesce(submitter.display_name, ''), coalesce(reviewed_by::text, ''), coalesce(review_note, ''),
			coalesce(last_check_status, ''), coalesce(last_check_error, ''), coalesce(session_secret, ''), coalesce(image_ref, ''), coalesce(image_digest, ''), container_port, coalesce(runtime_status, ''), coalesce(runtime_error, ''),
			submitted_at, coalesce(reviewed_at, '0001-01-01'::timestamptz), coalesce(last_checked_at, '0001-01-01'::timestamptz)
		from games
		left join players submitter on submitter.id = games.submitted_by
		where status = 'pending'
		order by submitted_at desc, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var games []Game
	for rows.Next() {
		game, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, game)
	}
	return games, rows.Err()
}

func (s *Store) SubmitGame(ctx context.Context, submission GameSubmission) (Game, error) {
	if submission.ContainerPort == 0 {
		submission.ContainerPort = 8081
	}
	if err := validateGameSubmission(submission); err != nil {
		return Game{}, err
	}
	row := s.pool.QueryRow(ctx, `
		insert into games (id, name, description, endpoint_url, protocol, min_cols, min_rows, max_players, supports_mouse, enabled, status, submitted_by, session_secret, image_ref, container_port, last_check_status, last_check_error, last_checked_at, runtime_status, runtime_error)
		values ($1, $2, $3, $4, 'ggp.cell.v1', $5, $6, $7, $8, false, 'pending', $9, $10, $11, $12, $13, $14, now(), 'pending-deploy', '')
		returning id, name, description, endpoint_url, protocol, min_cols, min_rows, max_players, supports_mouse,
			status, coalesce(submitted_by::text, ''), '', coalesce(reviewed_by::text, ''), coalesce(review_note, ''),
			coalesce(last_check_status, ''), coalesce(last_check_error, ''), coalesce(session_secret, ''), coalesce(image_ref, ''), coalesce(image_digest, ''), container_port, coalesce(runtime_status, ''), coalesce(runtime_error, ''),
			submitted_at, coalesce(reviewed_at, '0001-01-01'::timestamptz), coalesce(last_checked_at, '0001-01-01'::timestamptz)
	`, submission.ID, strings.TrimSpace(submission.Name), strings.TrimSpace(submission.Description), ImageEndpointURL(submission.ID, submission.ContainerPort), submission.MinCols, submission.MinRows, submission.MaxPlayers, submission.SupportsMouse, submission.SubmittedBy, strings.TrimSpace(submission.SessionSecret), strings.TrimSpace(submission.ImageRef), submission.ContainerPort, submission.LastCheckStatus, submission.LastCheckError)
	game, err := scanGame(row)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return Game{}, errors.New("a game with that ID already exists")
		}
		return Game{}, err
	}
	return game, nil
}

func (s *Store) ApproveGame(ctx context.Context, gameID string, reviewer Player) error {
	if reviewer.Role != RoleAdmin {
		return errors.New("admin role is required")
	}
	_, err := s.pool.Exec(ctx, `
		update games
		set status = 'approved', enabled = true, reviewed_by = $2, reviewed_at = now(), review_note = '', updated_at = now()
		where id = $1 and status = 'pending'
	`, gameID, reviewer.ID)
	return err
}

func (s *Store) RejectGame(ctx context.Context, gameID string, reviewer Player, note string) error {
	if reviewer.Role != RoleAdmin {
		return errors.New("admin role is required")
	}
	_, err := s.pool.Exec(ctx, `
		update games
		set status = 'rejected', enabled = false, reviewed_by = $2, reviewed_at = now(), review_note = $3, updated_at = now()
		where id = $1 and status = 'pending'
	`, gameID, reviewer.ID, strings.TrimSpace(note))
	return err
}

func (s *Store) UpdateGameCheck(ctx context.Context, gameID, status, message string) error {
	_, err := s.pool.Exec(ctx, `
		update games
		set last_check_status = $2, last_check_error = $3, last_checked_at = now(), updated_at = now()
		where id = $1
	`, gameID, status, strings.TrimSpace(message))
	return err
}

func (s *Store) RefreshPlayer(ctx context.Context, player Player) (Player, error) {
	err := s.pool.QueryRow(ctx, `
		select p.id::text, p.display_name, coalesce(k.fingerprint, ''), p.name_confirmed, p.role
		from players p
		left join ssh_keys k on k.player_id = p.id and k.revoked_at is null
		where p.id = $1
		limit 1
	`, player.ID).Scan(&player.ID, &player.DisplayName, &player.Fingerprint, &player.NameConfirmed, &player.Role)
	return player, err
}

func (s *Store) ListRunnableImageGames(ctx context.Context) ([]ImageGame, error) {
	rows, err := s.pool.Query(ctx, `
		select id, image_ref, container_port, coalesce(session_secret, ''), status
		from games
		where image_ref <> '' and status in ('pending', 'approved')
		order by id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var games []ImageGame
	for rows.Next() {
		var game ImageGame
		if err := rows.Scan(&game.ID, &game.ImageRef, &game.ContainerPort, &game.SessionSecret, &game.Status); err != nil {
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

func (s *Store) UpsertScore(ctx context.Context, gameID string, player Player, score int64) error {
	_, err := s.pool.Exec(ctx, `
		insert into leaderboard_scores (game_id, player_id, display_name, score)
		values ($1, $2, $3, $4)
		on conflict (game_id, player_id) do update set
			display_name = excluded.display_name,
			score = greatest(leaderboard_scores.score, excluded.score),
			updated_at = case
				when excluded.score > leaderboard_scores.score then now()
				else leaderboard_scores.updated_at
			end
	`, gameID, player.ID, player.DisplayName, score)
	return err
}

func (s *Store) Leaderboard(ctx context.Context, gameID string, limit int) ([]LeaderboardEntry, error) {
	rows, err := s.pool.Query(ctx, `
		select game_id, player_id::text, display_name, score, updated_at
		from leaderboard_scores
		where game_id = $1
		order by score desc, updated_at asc
		limit $2
	`, gameID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LeaderboardEntry
	for rows.Next() {
		var entry LeaderboardEntry
		if err := rows.Scan(&entry.GameID, &entry.PlayerID, &entry.DisplayName, &entry.Score, &entry.UpdatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func scanGame(row scanner) (Game, error) {
	var game Game
	err := row.Scan(
		&game.ID,
		&game.Name,
		&game.Description,
		&game.EndpointURL,
		&game.Protocol,
		&game.MinCols,
		&game.MinRows,
		&game.MaxPlayers,
		&game.SupportsMouse,
		&game.Status,
		&game.SubmittedBy,
		&game.SubmittedByName,
		&game.ReviewedBy,
		&game.ReviewNote,
		&game.LastCheckStatus,
		&game.LastCheckError,
		&game.SessionSecret,
		&game.ImageRef,
		&game.ImageDigest,
		&game.ContainerPort,
		&game.RuntimeStatus,
		&game.RuntimeError,
		&game.SubmittedAt,
		&game.ReviewedAt,
		&game.LastCheckedAt,
	)
	return game, err
}

func validateGameSubmission(submission GameSubmission) error {
	if !gameIDPattern.MatchString(submission.ID) {
		return errors.New("game ID must use lowercase letters, numbers, and hyphens")
	}
	if strings.TrimSpace(submission.Name) == "" {
		return errors.New("game name is required")
	}
	if len([]rune(submission.Name)) > 60 {
		return errors.New("game name must be 60 characters or fewer")
	}
	if strings.TrimSpace(submission.Description) == "" {
		return errors.New("game description is required")
	}
	if len([]rune(submission.Description)) > 240 {
		return errors.New("game description must be 240 characters or fewer")
	}
	if strings.TrimSpace(submission.ImageRef) == "" {
		return errors.New("Docker image is required")
	}
	if !imageRefPattern.MatchString(strings.TrimSpace(submission.ImageRef)) || strings.Contains(submission.ImageRef, " ") {
		return errors.New("Docker image must be a valid image reference")
	}
	if strings.HasSuffix(submission.ImageRef, ":latest") || !strings.ContainsAny(lastImageSegment(submission.ImageRef), ":@") {
		return errors.New("Docker image must be pinned with a non-latest tag or digest")
	}
	if submission.ContainerPort < 1 || submission.ContainerPort > 65535 {
		return errors.New("container port must be between 1 and 65535")
	}
	if submission.MinCols < 20 || submission.MinCols > 240 {
		return errors.New("min columns must be between 20 and 240")
	}
	if submission.MinRows < 8 || submission.MinRows > 80 {
		return errors.New("min rows must be between 8 and 80")
	}
	if submission.MaxPlayers < 1 || submission.MaxPlayers > 100 {
		return errors.New("max players must be between 1 and 100")
	}
	if submission.MaxPlayers > 1 && len(submission.SessionSecret) < 32 {
		return errors.New("multiplayer submissions require a 32+ byte game session secret")
	}
	return nil
}

func ImageEndpointURL(gameID string, port int) string {
	if port == 0 {
		port = 8081
	}
	return fmt.Sprintf("ws://gamegateway-game-%s:%d/ggp", gameID, port)
}

func lastImageSegment(imageRef string) string {
	if i := strings.LastIndex(imageRef, "/"); i >= 0 {
		return imageRef[i+1:]
	}
	return imageRef
}

func SlugifyGameID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteRune('-')
			lastDash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "game"
	}
	return slug
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

func (s *Store) migrateModeration(ctx context.Context) error {
	statements := []string{
		`alter table players add column if not exists role text not null default 'standard'`,
		`alter table games add column if not exists status text not null default 'approved'`,
		`alter table games add column if not exists submitted_by uuid references players(id) on delete set null`,
		`alter table games add column if not exists reviewed_by uuid references players(id) on delete set null`,
		`alter table games add column if not exists submitted_at timestamptz not null default now()`,
		`alter table games add column if not exists reviewed_at timestamptz`,
		`alter table games add column if not exists review_note text not null default ''`,
		`alter table games add column if not exists last_check_status text not null default ''`,
		`alter table games add column if not exists last_check_error text not null default ''`,
		`update games set last_check_error = '' where last_check_error is null`,
		`alter table games add column if not exists last_checked_at timestamptz`,
		`alter table games add column if not exists session_secret text not null default ''`,
		`alter table games add column if not exists image_ref text not null default ''`,
		`alter table games add column if not exists image_digest text not null default ''`,
		`alter table games add column if not exists container_port integer not null default 8081`,
		`alter table games add column if not exists runtime_status text not null default ''`,
		`alter table games add column if not exists runtime_error text not null default ''`,
		`update games set status = 'approved' where status = ''`,
		`update players set role = 'admin' where lower(display_name) in ('josh', 'ryan', 'ryanvogel')`,
	}
	for _, statement := range statements {
		if _, err := s.pool.Exec(ctx, statement); err != nil {
			return err
		}
	}

	constraints := map[string]string{
		"players_role_check": `alter table players add constraint players_role_check check (role in ('standard', 'admin'))`,
		"games_status_check": `alter table games add constraint games_status_check check (status in ('pending', 'approved', 'rejected', 'disabled'))`,
	}
	for name, statement := range constraints {
		var exists bool
		if err := s.pool.QueryRow(ctx, `select exists(select 1 from pg_constraint where conname = $1)`, name).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if _, err := s.pool.Exec(ctx, statement); err != nil {
				return err
			}
		}
	}
	return nil
}

const schemaSQL = `
create table if not exists players (
  id uuid primary key default gen_random_uuid(),
  display_name varchar(12) not null,
  name_confirmed boolean not null default false,
  role text not null default 'standard',
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
  status text not null default 'approved',
  submitted_by uuid references players(id) on delete set null,
  reviewed_by uuid references players(id) on delete set null,
  submitted_at timestamptz not null default now(),
  reviewed_at timestamptz,
  review_note text not null default '',
  last_check_status text not null default '',
  last_check_error text not null default '',
  last_checked_at timestamptz,
  session_secret text not null default '',
  image_ref text not null default '',
  image_digest text not null default '',
  container_port integer not null default 8081,
  runtime_status text not null default '',
  runtime_error text not null default '',
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

create table if not exists leaderboard_scores (
  game_id text not null references games(id) on delete cascade,
  player_id uuid not null references players(id) on delete cascade,
  display_name text not null,
  score bigint not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (game_id, player_id)
);

create index if not exists chat_messages_room_id_id_idx on chat_messages(room_id, id desc);
create index if not exists leaderboard_scores_game_id_score_idx on leaderboard_scores(game_id, score desc, updated_at asc);
`
