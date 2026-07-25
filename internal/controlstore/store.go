package controlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	DefaultRefreshIntervalSeconds = 5
	MinRefreshIntervalSeconds     = 2
	MaxRefreshIntervalSeconds     = 300
)

type Store struct {
	database *sql.DB
	now      func() time.Time
}

type Preferences struct {
	RefreshIntervalSeconds int    `json:"refreshIntervalSeconds"`
	InterfaceDensity       string `json:"interfaceDensity"`
	BaseFontSize           int    `json:"baseFontSize"`
	PageSize               int    `json:"pageSize"`
	SidebarDefault         string `json:"sidebarDefault"`
	LinkOpenMode           string `json:"linkOpenMode"`
	SiteDefaultProtocol    string `json:"siteDefaultProtocol"`
	ChineseFont            string `json:"chineseFont"`
	LatinFont              string `json:"latinFont"`
}

func DefaultPreferences() Preferences {
	return Preferences{
		RefreshIntervalSeconds: DefaultRefreshIntervalSeconds,
		InterfaceDensity:       "comfortable",
		BaseFontSize:           15,
		PageSize:               25,
		SidebarDefault:         "collapsed",
		LinkOpenMode:           "new-tab",
		SiteDefaultProtocol:    "http",
		ChineseFont:            "system",
		LatinFont:              "system",
	}
}

type DatabaseProjectPreference struct {
	ProjectKey string `json:"projectKey"`
	Archived   bool   `json:"archived"`
}

type SiteProfile struct {
	ProjectID     string     `json:"projectId"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	IconURL       string     `json:"iconUrl"`
	Category      string     `json:"category"`
	PrimaryPort   int        `json:"primaryPort"`
	LaunchURL     string     `json:"launchUrl"`
	Favorite      bool       `json:"favorite"`
	SortOrder     int        `json:"sortOrder"`
	LastVisitedAt *time.Time `json:"lastVisitedAt"`
	Hidden        bool       `json:"hidden"`
}

type ComposeDraft struct {
	ProjectID   string    `json:"projectId"`
	ConfigPath  string    `json:"configPath"`
	Content     string    `json:"content"`
	ContentHash string    `json:"contentHash"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type ComposeRevision struct {
	ID          int64     `json:"id"`
	ProjectID   string    `json:"projectId"`
	ConfigPath  string    `json:"configPath"`
	Content     string    `json:"content"`
	ContentHash string    `json:"contentHash"`
	BackupPath  string    `json:"backupPath"`
	CreatedAt   time.Time `json:"createdAt"`
}

type MetricSample struct {
	CollectedAt     time.Time `json:"collectedAt"`
	CPUPercent      float64   `json:"cpuPercent"`
	MemoryPercent   float64   `json:"memoryPercent"`
	Load1           float64   `json:"load1"`
	DiskPercent     float64   `json:"diskPercent"`
	NetworkReceive  uint64    `json:"networkReceiveBytes"`
	NetworkTransmit uint64    `json:"networkTransmitBytes"`
}

func Open(databasePath string) (*Store, error) {
	if strings.TrimSpace(databasePath) == "" {
		return nil, errors.New("control store database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o750); err != nil {
		return nil, err
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	store := &Store{database: database, now: time.Now}
	if err := store.migrate(context.Background()); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.database == nil {
		return nil
	}
	return s.database.Close()
}

func (s *Store) Preferences(ctx context.Context, userID int64) (Preferences, error) {
	var result Preferences
	err := s.database.QueryRowContext(ctx, `
		SELECT refresh_interval_seconds, interface_density, base_font_size, page_size,
			sidebar_default, link_open_mode, site_default_protocol, chinese_font, latin_font
		FROM user_preferences
		WHERE user_id = ?
	`, userID).Scan(
		&result.RefreshIntervalSeconds, &result.InterfaceDensity, &result.BaseFontSize, &result.PageSize,
		&result.SidebarDefault, &result.LinkOpenMode, &result.SiteDefaultProtocol, &result.ChineseFont, &result.LatinFont,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultPreferences(), nil
	}
	return result, err
}

func (s *Store) UpdatePreferences(ctx context.Context, userID int64, preferences Preferences) (Preferences, error) {
	if err := validatePreferences(preferences); err != nil {
		return Preferences{}, err
	}
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO user_preferences (
			user_id, refresh_interval_seconds, interface_density, base_font_size, page_size,
			sidebar_default, link_open_mode, site_default_protocol, chinese_font, latin_font, updated_at_unix
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			refresh_interval_seconds = excluded.refresh_interval_seconds,
			interface_density = excluded.interface_density,
			base_font_size = excluded.base_font_size,
			page_size = excluded.page_size,
			sidebar_default = excluded.sidebar_default,
			link_open_mode = excluded.link_open_mode,
			site_default_protocol = excluded.site_default_protocol,
			chinese_font = excluded.chinese_font,
			latin_font = excluded.latin_font,
			updated_at_unix = excluded.updated_at_unix
	`, userID, preferences.RefreshIntervalSeconds, preferences.InterfaceDensity, preferences.BaseFontSize, preferences.PageSize,
		preferences.SidebarDefault, preferences.LinkOpenMode, preferences.SiteDefaultProtocol, preferences.ChineseFont,
		preferences.LatinFont, s.now().UTC().Unix())
	if err != nil {
		return Preferences{}, err
	}
	return preferences, nil
}

func (s *Store) RecordMetricSample(ctx context.Context, sample MetricSample) error {
	bucket := sample.CollectedAt.UTC().Unix() / 60 * 60
	_, err := s.database.ExecContext(ctx, `
		INSERT OR IGNORE INTO metric_samples (
			collected_at_unix, cpu_percent, memory_percent, load1, disk_percent,
			network_receive_bytes, network_transmit_bytes
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, bucket, sample.CPUPercent, sample.MemoryPercent, sample.Load1, sample.DiskPercent,
		sample.NetworkReceive, sample.NetworkTransmit)
	if err != nil {
		return err
	}
	_, err = s.database.ExecContext(ctx, `DELETE FROM metric_samples WHERE collected_at_unix < ?`, s.now().UTC().Add(-7*24*time.Hour).Unix())
	return err
}

func (s *Store) MetricSamples(ctx context.Context, since time.Time) ([]MetricSample, error) {
	rows, err := s.database.QueryContext(ctx, `
		SELECT collected_at_unix, cpu_percent, memory_percent, load1, disk_percent,
			network_receive_bytes, network_transmit_bytes
		FROM metric_samples
		WHERE collected_at_unix >= ?
		ORDER BY collected_at_unix
	`, since.UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	samples := make([]MetricSample, 0)
	for rows.Next() {
		var sample MetricSample
		var collectedAtUnix int64
		if err := rows.Scan(&collectedAtUnix, &sample.CPUPercent, &sample.MemoryPercent, &sample.Load1, &sample.DiskPercent,
			&sample.NetworkReceive, &sample.NetworkTransmit); err != nil {
			return nil, err
		}
		sample.CollectedAt = time.Unix(collectedAtUnix, 0).UTC()
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}

func validatePreferences(preferences Preferences) error {
	if preferences.RefreshIntervalSeconds < MinRefreshIntervalSeconds || preferences.RefreshIntervalSeconds > MaxRefreshIntervalSeconds {
		return errors.New("refresh interval is out of range")
	}
	if preferences.BaseFontSize < 13 || preferences.BaseFontSize > 18 {
		return errors.New("base font size is out of range")
	}
	if preferences.PageSize != 20 && preferences.PageSize != 25 && preferences.PageSize != 50 && preferences.PageSize != 100 {
		return errors.New("page size is invalid")
	}
	if !oneOf(preferences.InterfaceDensity, "comfortable", "compact") ||
		!oneOf(preferences.SidebarDefault, "collapsed", "expanded") ||
		!oneOf(preferences.LinkOpenMode, "new-tab", "same-tab") ||
		!oneOf(preferences.SiteDefaultProtocol, "http", "https") ||
		!oneOf(preferences.ChineseFont, "system", "noto-sans-sc") ||
		!oneOf(preferences.LatinFont, "system", "manrope") {
		return errors.New("preference option is invalid")
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func (s *Store) DatabaseProjectPreferences(ctx context.Context) ([]DatabaseProjectPreference, error) {
	rows, err := s.database.QueryContext(ctx, `
		SELECT project_key, archived
		FROM database_project_preferences
		ORDER BY project_key
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]DatabaseProjectPreference, 0)
	for rows.Next() {
		var preference DatabaseProjectPreference
		if err := rows.Scan(&preference.ProjectKey, &preference.Archived); err != nil {
			return nil, err
		}
		result = append(result, preference)
	}
	return result, rows.Err()
}

func (s *Store) SetDatabaseProjectPreference(ctx context.Context, preference DatabaseProjectPreference) (DatabaseProjectPreference, error) {
	preference.ProjectKey = strings.TrimSpace(preference.ProjectKey)
	if preference.ProjectKey == "" || len(preference.ProjectKey) > 512 {
		return DatabaseProjectPreference{}, errors.New("database project key is invalid")
	}
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO database_project_preferences (project_key, archived, updated_at_unix)
		VALUES (?, ?, ?)
		ON CONFLICT(project_key) DO UPDATE SET
			archived = excluded.archived,
			updated_at_unix = excluded.updated_at_unix
	`, preference.ProjectKey, preference.Archived, s.now().UTC().Unix())
	if err != nil {
		return DatabaseProjectPreference{}, err
	}
	return preference, nil
}

func (s *Store) SiteProfiles(ctx context.Context) ([]SiteProfile, error) {
	rows, err := s.database.QueryContext(ctx, `
		SELECT project_id, name, description, icon_url, category, primary_port,
			launch_url, favorite, sort_order, last_visited_at_unix, hidden
		FROM site_profiles
		ORDER BY updated_at_unix ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]SiteProfile, 0)
	for rows.Next() {
		var profile SiteProfile
		var lastVisitedAt sql.NullInt64
		if err := rows.Scan(
			&profile.ProjectID,
			&profile.Name,
			&profile.Description,
			&profile.IconURL,
			&profile.Category,
			&profile.PrimaryPort,
			&profile.LaunchURL,
			&profile.Favorite,
			&profile.SortOrder,
			&lastVisitedAt,
			&profile.Hidden,
		); err != nil {
			return nil, err
		}
		if lastVisitedAt.Valid && lastVisitedAt.Int64 > 0 {
			value := time.Unix(lastVisitedAt.Int64, 0).UTC()
			profile.LastVisitedAt = &value
		}
		if decodedProjectID, err := url.PathUnescape(profile.ProjectID); err == nil {
			profile.ProjectID = decodedProjectID
		}
		result = append(result, profile)
	}
	return result, rows.Err()
}

func (s *Store) UpsertSiteProfile(ctx context.Context, profile SiteProfile) (SiteProfile, error) {
	profile.ProjectID = strings.TrimSpace(profile.ProjectID)
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Description = strings.TrimSpace(profile.Description)
	profile.IconURL = strings.TrimSpace(profile.IconURL)
	profile.Category = strings.TrimSpace(profile.Category)
	profile.LaunchURL = strings.TrimSpace(profile.LaunchURL)
	if profile.ProjectID == "" || len(profile.ProjectID) > 256 || profile.Name == "" || len(profile.Name) > 120 {
		return SiteProfile{}, errors.New("site profile is invalid")
	}
	if len(profile.Description) > 500 || len(profile.IconURL) > 2048 || len(profile.Category) > 64 || len(profile.LaunchURL) > 2048 || profile.PrimaryPort < 0 || profile.PrimaryPort > 65535 || profile.SortOrder < -100000 || profile.SortOrder > 100000 {
		return SiteProfile{}, errors.New("site profile is invalid")
	}
	if profile.LaunchURL != "" {
		parsed, err := url.ParseRequestURI(profile.LaunchURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return SiteProfile{}, errors.New("site launch URL is invalid")
		}
	}
	if profile.IconURL != "" {
		parsed, err := url.ParseRequestURI(profile.IconURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return SiteProfile{}, errors.New("site icon URL is invalid")
		}
	}
	lastVisitedAtUnix := int64(0)
	if profile.LastVisitedAt != nil {
		lastVisitedAtUnix = profile.LastVisitedAt.UTC().Unix()
	}
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO site_profiles (
			project_id, name, description, icon_url, category, primary_port,
			launch_url, favorite, sort_order, last_visited_at_unix, hidden, updated_at_unix
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET
			name = excluded.name,
			description = excluded.description,
			icon_url = excluded.icon_url,
			category = excluded.category,
			primary_port = excluded.primary_port,
			launch_url = excluded.launch_url,
			favorite = excluded.favorite,
			sort_order = excluded.sort_order,
			hidden = excluded.hidden,
			updated_at_unix = excluded.updated_at_unix
	`, profile.ProjectID, profile.Name, profile.Description, profile.IconURL, profile.Category, profile.PrimaryPort,
		profile.LaunchURL, profile.Favorite, profile.SortOrder, lastVisitedAtUnix, profile.Hidden, s.now().UTC().Unix())
	if err != nil {
		return SiteProfile{}, err
	}
	return profile, nil
}

func (s *Store) RecordSiteVisit(ctx context.Context, projectID string) (time.Time, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" || len(projectID) > 256 {
		return time.Time{}, errors.New("site project id is invalid")
	}
	visitedAt := s.now().UTC().Truncate(time.Second)
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO site_profiles (project_id, name, last_visited_at_unix, updated_at_unix)
		VALUES (?, '', ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET
			last_visited_at_unix = excluded.last_visited_at_unix,
			updated_at_unix = excluded.updated_at_unix
	`, projectID, visitedAt.Unix(), visitedAt.Unix())
	return visitedAt, err
}

func (s *Store) ComposeDraft(ctx context.Context, projectID, configPath string) (ComposeDraft, error) {
	projectID, configPath = strings.TrimSpace(projectID), strings.TrimSpace(configPath)
	var draft ComposeDraft
	var updatedAtUnix int64
	err := s.database.QueryRowContext(ctx, `
		SELECT project_id, config_path, content, content_hash, updated_at_unix
		FROM compose_drafts
		WHERE project_id = ? AND config_path = ?
	`, projectID, configPath).Scan(&draft.ProjectID, &draft.ConfigPath, &draft.Content, &draft.ContentHash, &updatedAtUnix)
	if err != nil {
		return ComposeDraft{}, err
	}
	draft.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()
	return draft, nil
}

func (s *Store) SaveComposeDraft(ctx context.Context, draft ComposeDraft) (ComposeDraft, error) {
	draft.ProjectID = strings.TrimSpace(draft.ProjectID)
	draft.ConfigPath = strings.TrimSpace(draft.ConfigPath)
	if draft.ProjectID == "" || len(draft.ProjectID) > 256 || draft.ConfigPath == "" || len(draft.ConfigPath) > 2048 || draft.Content == "" || len(draft.Content) > 2<<20 {
		return ComposeDraft{}, errors.New("compose draft is invalid")
	}
	sum := sha256.Sum256([]byte(draft.Content))
	draft.ContentHash = hex.EncodeToString(sum[:])
	draft.UpdatedAt = s.now().UTC().Truncate(time.Second)
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO compose_drafts (project_id, config_path, content, content_hash, updated_at_unix)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(project_id, config_path) DO UPDATE SET
			content = excluded.content,
			content_hash = excluded.content_hash,
			updated_at_unix = excluded.updated_at_unix
	`, draft.ProjectID, draft.ConfigPath, draft.Content, draft.ContentHash, draft.UpdatedAt.Unix())
	if err != nil {
		return ComposeDraft{}, err
	}
	return draft, nil
}

func (s *Store) RecordComposeRevision(ctx context.Context, revision ComposeRevision) (ComposeRevision, error) {
	revision.ProjectID = strings.TrimSpace(revision.ProjectID)
	revision.ConfigPath = strings.TrimSpace(revision.ConfigPath)
	revision.BackupPath = strings.TrimSpace(revision.BackupPath)
	if revision.ProjectID == "" || revision.ConfigPath == "" || revision.Content == "" || revision.BackupPath == "" {
		return ComposeRevision{}, errors.New("compose revision is invalid")
	}
	sum := sha256.Sum256([]byte(revision.Content))
	revision.ContentHash = hex.EncodeToString(sum[:])
	revision.CreatedAt = s.now().UTC().Truncate(time.Second)
	result, err := s.database.ExecContext(ctx, `
		INSERT INTO compose_revisions (project_id, config_path, content, content_hash, backup_path, created_at_unix)
		VALUES (?, ?, ?, ?, ?, ?)
	`, revision.ProjectID, revision.ConfigPath, revision.Content, revision.ContentHash, revision.BackupPath, revision.CreatedAt.Unix())
	if err != nil {
		return ComposeRevision{}, err
	}
	revision.ID, err = result.LastInsertId()
	return revision, err
}

func (s *Store) ComposeRevisions(ctx context.Context, projectID string, limit int) ([]ComposeRevision, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.database.QueryContext(ctx, `
		SELECT id, project_id, config_path, content, content_hash, backup_path, created_at_unix
		FROM compose_revisions
		WHERE project_id = ?
		ORDER BY id DESC
		LIMIT ?
	`, strings.TrimSpace(projectID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	revisions := make([]ComposeRevision, 0)
	for rows.Next() {
		var revision ComposeRevision
		var createdAtUnix int64
		if err := rows.Scan(&revision.ID, &revision.ProjectID, &revision.ConfigPath, &revision.Content, &revision.ContentHash, &revision.BackupPath, &createdAtUnix); err != nil {
			return nil, err
		}
		revision.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS user_preferences (
			user_id INTEGER PRIMARY KEY,
			refresh_interval_seconds INTEGER NOT NULL DEFAULT 5,
			updated_at_unix INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS database_project_preferences (
			project_key TEXT PRIMARY KEY,
			archived INTEGER NOT NULL DEFAULT 0,
			updated_at_unix INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS site_profiles (
			project_id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			icon_url TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT '',
			primary_port INTEGER NOT NULL DEFAULT 0,
			hidden INTEGER NOT NULL DEFAULT 0,
			updated_at_unix INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS compose_drafts (
			project_id TEXT NOT NULL,
			config_path TEXT NOT NULL,
			content TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			updated_at_unix INTEGER NOT NULL,
			PRIMARY KEY (project_id, config_path)
		)`,
		`CREATE TABLE IF NOT EXISTS compose_revisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id TEXT NOT NULL,
			config_path TEXT NOT NULL,
			content TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			backup_path TEXT NOT NULL,
			created_at_unix INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS compose_revisions_project_created
			ON compose_revisions(project_id, created_at_unix DESC)`,
		`CREATE TABLE IF NOT EXISTS metric_samples (
			collected_at_unix INTEGER PRIMARY KEY,
			cpu_percent REAL NOT NULL,
			memory_percent REAL NOT NULL,
			load1 REAL NOT NULL,
			disk_percent REAL NOT NULL,
			network_receive_bytes INTEGER NOT NULL,
			network_transmit_bytes INTEGER NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := s.database.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	siteColumns := []struct {
		name       string
		definition string
	}{
		{name: "launch_url", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "favorite", definition: "INTEGER NOT NULL DEFAULT 0"},
		{name: "sort_order", definition: "INTEGER NOT NULL DEFAULT 0"},
		{name: "last_visited_at_unix", definition: "INTEGER NOT NULL DEFAULT 0"},
	}
	preferenceColumns := []struct {
		name       string
		definition string
	}{
		{name: "interface_density", definition: "TEXT NOT NULL DEFAULT 'comfortable'"},
		{name: "base_font_size", definition: "INTEGER NOT NULL DEFAULT 15"},
		{name: "page_size", definition: "INTEGER NOT NULL DEFAULT 25"},
		{name: "sidebar_default", definition: "TEXT NOT NULL DEFAULT 'collapsed'"},
		{name: "link_open_mode", definition: "TEXT NOT NULL DEFAULT 'new-tab'"},
		{name: "site_default_protocol", definition: "TEXT NOT NULL DEFAULT 'http'"},
		{name: "chinese_font", definition: "TEXT NOT NULL DEFAULT 'system'"},
		{name: "latin_font", definition: "TEXT NOT NULL DEFAULT 'system'"},
	}
	for _, column := range preferenceColumns {
		if err := s.ensureColumn(ctx, "user_preferences", column.name, column.definition); err != nil {
			return err
		}
	}
	for _, column := range siteColumns {
		if err := s.ensureColumn(ctx, "site_profiles", column.name, column.definition); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table string, column string, definition string) error {
	rows, err := s.database.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.database.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	return err
}
