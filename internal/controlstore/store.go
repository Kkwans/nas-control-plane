package controlstore

import (
	"context"
	"database/sql"
	"errors"
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
	RefreshIntervalSeconds int `json:"refreshIntervalSeconds"`
}

type DatabaseProjectPreference struct {
	ProjectKey string `json:"projectKey"`
	Archived   bool   `json:"archived"`
}

type SiteProfile struct {
	ProjectID   string `json:"projectId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IconURL     string `json:"iconUrl"`
	Category    string `json:"category"`
	PrimaryPort int    `json:"primaryPort"`
	Hidden      bool   `json:"hidden"`
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
		SELECT refresh_interval_seconds
		FROM user_preferences
		WHERE user_id = ?
	`, userID).Scan(&result.RefreshIntervalSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return Preferences{RefreshIntervalSeconds: DefaultRefreshIntervalSeconds}, nil
	}
	return result, err
}

func (s *Store) UpdatePreferences(ctx context.Context, userID int64, preferences Preferences) (Preferences, error) {
	if preferences.RefreshIntervalSeconds < MinRefreshIntervalSeconds || preferences.RefreshIntervalSeconds > MaxRefreshIntervalSeconds {
		return Preferences{}, errors.New("refresh interval is out of range")
	}
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO user_preferences (user_id, refresh_interval_seconds, updated_at_unix)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			refresh_interval_seconds = excluded.refresh_interval_seconds,
			updated_at_unix = excluded.updated_at_unix
	`, userID, preferences.RefreshIntervalSeconds, s.now().UTC().Unix())
	if err != nil {
		return Preferences{}, err
	}
	return preferences, nil
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
		SELECT project_id, name, description, icon_url, category, primary_port, hidden
		FROM site_profiles
		ORDER BY name COLLATE NOCASE
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]SiteProfile, 0)
	for rows.Next() {
		var profile SiteProfile
		if err := rows.Scan(
			&profile.ProjectID,
			&profile.Name,
			&profile.Description,
			&profile.IconURL,
			&profile.Category,
			&profile.PrimaryPort,
			&profile.Hidden,
		); err != nil {
			return nil, err
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
	if profile.ProjectID == "" || len(profile.ProjectID) > 256 || profile.Name == "" || len(profile.Name) > 120 {
		return SiteProfile{}, errors.New("site profile is invalid")
	}
	if len(profile.Description) > 500 || len(profile.IconURL) > 2048 || len(profile.Category) > 64 || profile.PrimaryPort < 0 || profile.PrimaryPort > 65535 {
		return SiteProfile{}, errors.New("site profile is invalid")
	}
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO site_profiles (
			project_id, name, description, icon_url, category, primary_port, hidden, updated_at_unix
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET
			name = excluded.name,
			description = excluded.description,
			icon_url = excluded.icon_url,
			category = excluded.category,
			primary_port = excluded.primary_port,
			hidden = excluded.hidden,
			updated_at_unix = excluded.updated_at_unix
	`, profile.ProjectID, profile.Name, profile.Description, profile.IconURL, profile.Category, profile.PrimaryPort, profile.Hidden, s.now().UTC().Unix())
	if err != nil {
		return SiteProfile{}, err
	}
	return profile, nil
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
	}
	for _, statement := range statements {
		if _, err := s.database.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
