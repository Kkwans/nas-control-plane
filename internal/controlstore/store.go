package controlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

var defaultNavigationOrder = []string{
	"overview",
	"sites",
	"docker",
	"databases",
	"logs",
	"monitoring",
	"system",
	"users",
	"terminal",
	"settings",
}

var validNavigationIDs = map[string]struct{}{
	"overview":   {},
	"sites":      {},
	"docker":     {},
	"databases":  {},
	"logs":       {},
	"monitoring": {},
	"system":     {},
	"users":      {},
	"terminal":   {},
	"settings":   {},
}

type Store struct {
	database *sql.DB
	now      func() time.Time
}

type Preferences struct {
	RefreshIntervalSeconds int      `json:"refreshIntervalSeconds"`
	InterfaceDensity       string   `json:"interfaceDensity"`
	BaseFontSize           int      `json:"baseFontSize"`
	PageSize               int      `json:"pageSize"`
	SidebarDefault         string   `json:"sidebarDefault"`
	LinkOpenMode           string   `json:"linkOpenMode"`
	SiteDefaultProtocol    string   `json:"siteDefaultProtocol"`
	ChineseFont            string   `json:"chineseFont"`
	LatinFont              string   `json:"latinFont"`
	NavigationOrder        []string `json:"navigationOrder"`
}

func DefaultPreferences() Preferences {
	return Preferences{
		RefreshIntervalSeconds: DefaultRefreshIntervalSeconds,
		InterfaceDensity:       "comfortable",
		BaseFontSize:           15,
		PageSize:               10,
		SidebarDefault:         "collapsed",
		LinkOpenMode:           "new-tab",
		SiteDefaultProtocol:    "http",
		ChineseFont:            "system",
		LatinFont:              "system",
		NavigationOrder:        defaultNavigationOrderCopy(),
	}
}

func defaultNavigationOrderCopy() []string {
	return append([]string(nil), defaultNavigationOrder...)
}

func normalizeNavigationOrder(value []string) []string {
	result := make([]string, 0, len(defaultNavigationOrder))
	seen := make(map[string]struct{}, len(defaultNavigationOrder))
	candidates := append(append([]string(nil), value...), defaultNavigationOrder...)
	for _, candidate := range candidates {
		id := strings.TrimSpace(candidate)
		if _, valid := validNavigationIDs[id]; !valid {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func encodeNavigationOrder(value []string) string {
	encoded, err := json.Marshal(normalizeNavigationOrder(value))
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func decodeNavigationOrder(value string) []string {
	var result []string
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return defaultNavigationOrderCopy()
	}
	return normalizeNavigationOrder(result)
}

type ListPreference struct {
	ListKey       string `json:"listKey"`
	PageSize      int    `json:"pageSize"`
	SortKey       string `json:"sortKey"`
	SortDirection string `json:"sortDirection"`
}

func DefaultListPreference(listKey string) ListPreference {
	return ListPreference{
		ListKey:       strings.TrimSpace(listKey),
		PageSize:      10,
		SortDirection: "asc",
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
	Source        string     `json:"source"`
	Ignored       bool       `json:"ignored"`
	DetectedTitle string     `json:"detectedTitle"`
	AutoIconURL   string     `json:"autoIconUrl"`
	LocalIconName string     `json:"localIconName"`
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

type JobRecord struct {
	ID              string    `json:"id"`
	Type            string    `json:"type"`
	Status          string    `json:"status"`
	Reference       string    `json:"reference"`
	Message         string    `json:"message"`
	Progress        int       `json:"progress"`
	Error           string    `json:"error"`
	DownloadedBytes int64     `json:"downloadedBytes"`
	TotalBytes      int64     `json:"totalBytes"`
	SpeedBytes      int64     `json:"speedBytes"`
	LayersJSON      string    `json:"layersJson"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	CompletedAt     time.Time `json:"completedAt"`
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
	var navigationOrder string
	err := s.database.QueryRowContext(ctx, `
		SELECT refresh_interval_seconds, interface_density, base_font_size, page_size,
			sidebar_default, link_open_mode, site_default_protocol, chinese_font, latin_font, navigation_order
		FROM user_preferences
		WHERE user_id = ?
	`, userID).Scan(
		&result.RefreshIntervalSeconds, &result.InterfaceDensity, &result.BaseFontSize, &result.PageSize,
		&result.SidebarDefault, &result.LinkOpenMode, &result.SiteDefaultProtocol, &result.ChineseFont, &result.LatinFont,
		&navigationOrder,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultPreferences(), nil
	}
	if err != nil {
		return Preferences{}, err
	}
	result.NavigationOrder = decodeNavigationOrder(navigationOrder)
	return result, nil
}

func (s *Store) UpdatePreferences(ctx context.Context, userID int64, preferences Preferences) (Preferences, error) {
	preferences.NavigationOrder = normalizeNavigationOrder(preferences.NavigationOrder)
	if err := validatePreferences(preferences); err != nil {
		return Preferences{}, err
	}
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO user_preferences (
			user_id, refresh_interval_seconds, interface_density, base_font_size, page_size,
			sidebar_default, link_open_mode, site_default_protocol, chinese_font, latin_font, navigation_order, updated_at_unix
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			navigation_order = excluded.navigation_order,
			updated_at_unix = excluded.updated_at_unix
	`, userID, preferences.RefreshIntervalSeconds, preferences.InterfaceDensity, preferences.BaseFontSize, preferences.PageSize,
		preferences.SidebarDefault, preferences.LinkOpenMode, preferences.SiteDefaultProtocol, preferences.ChineseFont,
		preferences.LatinFont, encodeNavigationOrder(preferences.NavigationOrder), s.now().UTC().Unix())
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
	if preferences.PageSize < 1 || preferences.PageSize > 200 {
		return errors.New("page size is invalid")
	}
	if !oneOf(preferences.InterfaceDensity, "comfortable", "compact") ||
		!oneOf(preferences.SidebarDefault, "collapsed", "expanded") ||
		!oneOf(preferences.LinkOpenMode, "new-tab", "same-tab") ||
		!oneOf(preferences.SiteDefaultProtocol, "http", "https") ||
		!oneOf(preferences.ChineseFont, "system", "noto-sans-sc", "microsoft-yahei", "source-han-sans-sc", "misans", "harmonyos-sans-sc") ||
		!oneOf(preferences.LatinFont, "system", "manrope", "inter", "ibm-plex-sans") {
		return errors.New("preference option is invalid")
	}
	return nil
}

func (s *Store) ListPreference(ctx context.Context, userID int64, listKey string) (ListPreference, error) {
	preference := DefaultListPreference(listKey)
	if err := validateListPreference(preference); err != nil {
		return ListPreference{}, err
	}
	err := s.database.QueryRowContext(ctx, `
		SELECT page_size, sort_key, sort_direction
		FROM list_preferences
		WHERE user_id = ? AND list_key = ?
	`, userID, preference.ListKey).Scan(&preference.PageSize, &preference.SortKey, &preference.SortDirection)
	if errors.Is(err, sql.ErrNoRows) {
		return preference, nil
	}
	return preference, err
}

func (s *Store) UpdateListPreference(ctx context.Context, userID int64, preference ListPreference) (ListPreference, error) {
	preference.ListKey = strings.TrimSpace(preference.ListKey)
	preference.SortKey = strings.TrimSpace(preference.SortKey)
	preference.SortDirection = strings.ToLower(strings.TrimSpace(preference.SortDirection))
	if err := validateListPreference(preference); err != nil {
		return ListPreference{}, err
	}
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO list_preferences (
			user_id, list_key, page_size, sort_key, sort_direction, updated_at_unix
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, list_key) DO UPDATE SET
			page_size = excluded.page_size,
			sort_key = excluded.sort_key,
			sort_direction = excluded.sort_direction,
			updated_at_unix = excluded.updated_at_unix
	`, userID, preference.ListKey, preference.PageSize, preference.SortKey, preference.SortDirection, s.now().UTC().Unix())
	if err != nil {
		return ListPreference{}, err
	}
	return preference, nil
}

func validateListPreference(preference ListPreference) error {
	if !validPreferenceKey(preference.ListKey) || preference.PageSize < 1 || preference.PageSize > 200 {
		return errors.New("list preference is invalid")
	}
	if preference.SortKey != "" && !validPreferenceKey(preference.SortKey) {
		return errors.New("list sort key is invalid")
	}
	if preference.SortDirection != "asc" && preference.SortDirection != "desc" {
		return errors.New("list sort direction is invalid")
	}
	return nil
}

func validPreferenceKey(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			(index > 0 && (character == '.' || character == '-' || character == '_')) {
			continue
		}
		return false
	}
	return true
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
			launch_url, favorite, sort_order, last_visited_at_unix, hidden,
			source, ignored, detected_title, auto_icon_url, local_icon_name
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
			&profile.Source,
			&profile.Ignored,
			&profile.DetectedTitle,
			&profile.AutoIconURL,
			&profile.LocalIconName,
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
	profile.Source = strings.ToLower(strings.TrimSpace(profile.Source))
	profile.DetectedTitle = strings.TrimSpace(profile.DetectedTitle)
	profile.AutoIconURL = strings.TrimSpace(profile.AutoIconURL)
	profile.LocalIconName = strings.TrimSpace(profile.LocalIconName)
	if profile.ProjectID == "" || len(profile.ProjectID) > 256 || profile.Name == "" || len(profile.Name) > 120 {
		return SiteProfile{}, errors.New("site profile is invalid")
	}
	if len(profile.Description) > 500 || len(profile.IconURL) > 2048 || len(profile.Category) > 64 || len(profile.LaunchURL) > 2048 || len(profile.DetectedTitle) > 200 || len(profile.AutoIconURL) > 2048 || len(profile.LocalIconName) > 160 || profile.PrimaryPort < 0 || profile.PrimaryPort > 65535 || profile.SortOrder < -100000 || profile.SortOrder > 100000 {
		return SiteProfile{}, errors.New("site profile is invalid")
	}
	if profile.Source == "" {
		profile.Source = "edited"
	}
	if profile.Source != "auto" && profile.Source != "manual" && profile.Source != "edited" {
		return SiteProfile{}, errors.New("site source is invalid")
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
			launch_url, favorite, sort_order, last_visited_at_unix, hidden,
			source, ignored, detected_title, auto_icon_url, local_icon_name, updated_at_unix
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			source = excluded.source,
			ignored = excluded.ignored,
			detected_title = excluded.detected_title,
			auto_icon_url = excluded.auto_icon_url,
			local_icon_name = excluded.local_icon_name,
			updated_at_unix = excluded.updated_at_unix
	`, profile.ProjectID, profile.Name, profile.Description, profile.IconURL, profile.Category, profile.PrimaryPort,
		profile.LaunchURL, profile.Favorite, profile.SortOrder, lastVisitedAtUnix, profile.Hidden,
		profile.Source, profile.Ignored, profile.DetectedTitle, profile.AutoIconURL, profile.LocalIconName, s.now().UTC().Unix())
	if err != nil {
		return SiteProfile{}, err
	}
	return profile, nil
}

func (s *Store) DeleteSiteProfile(ctx context.Context, siteID string) error {
	siteID = strings.TrimSpace(siteID)
	if siteID == "" || len(siteID) > 256 {
		return errors.New("site id is invalid")
	}
	_, err := s.database.ExecContext(ctx, `DELETE FROM site_profiles WHERE project_id = ?`, siteID)
	return err
}

func (s *Store) SetSiteIgnored(ctx context.Context, siteID string, ignored bool) error {
	siteID = strings.TrimSpace(siteID)
	if siteID == "" || len(siteID) > 256 {
		return errors.New("site id is invalid")
	}
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO site_profiles (project_id, name, source, ignored, updated_at_unix)
		VALUES (?, '', 'auto', ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET
			ignored = excluded.ignored,
			updated_at_unix = excluded.updated_at_unix
	`, siteID, ignored, s.now().UTC().Unix())
	return err
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

func (s *Store) UpsertJob(ctx context.Context, job JobRecord) error {
	completedAt := int64(0)
	if !job.CompletedAt.IsZero() {
		completedAt = job.CompletedAt.UTC().Unix()
	}
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO jobs (
			id, type, status, reference, message, error, progress,
			downloaded_bytes, total_bytes, speed_bytes, layers_json,
			created_at_unix, updated_at_unix, completed_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status = excluded.status,
			message = excluded.message,
			error = excluded.error,
			progress = excluded.progress,
			downloaded_bytes = excluded.downloaded_bytes,
			total_bytes = excluded.total_bytes,
			speed_bytes = excluded.speed_bytes,
			layers_json = excluded.layers_json,
			updated_at_unix = excluded.updated_at_unix,
			completed_at_unix = excluded.completed_at_unix
	`, job.ID, job.Type, job.Status, job.Reference, job.Message, job.Error, job.Progress,
		job.DownloadedBytes, job.TotalBytes, job.SpeedBytes, job.LayersJSON,
		job.CreatedAt.UTC().Unix(), job.UpdatedAt.UTC().Unix(), completedAt)
	return err
}

func (s *Store) Jobs(ctx context.Context, kind string, limit int) ([]JobRecord, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.database.QueryContext(ctx, `
		SELECT id, type, status, reference, message, error, progress,
			downloaded_bytes, total_bytes, speed_bytes, layers_json,
			created_at_unix, updated_at_unix, completed_at_unix
		FROM jobs
		WHERE (? = '' OR type = ?)
		ORDER BY created_at_unix DESC
		LIMIT ?
	`, kind, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]JobRecord, 0)
	for rows.Next() {
		var job JobRecord
		var createdAt, updatedAt, completedAt int64
		if err := rows.Scan(
			&job.ID, &job.Type, &job.Status, &job.Reference, &job.Message, &job.Error, &job.Progress,
			&job.DownloadedBytes, &job.TotalBytes, &job.SpeedBytes, &job.LayersJSON,
			&createdAt, &updatedAt, &completedAt,
		); err != nil {
			return nil, err
		}
		job.CreatedAt = time.Unix(createdAt, 0).UTC()
		job.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		if completedAt > 0 {
			job.CompletedAt = time.Unix(completedAt, 0).UTC()
		}
		result = append(result, job)
	}
	return result, rows.Err()
}

func (s *Store) DeleteJob(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("job id is required")
	}
	_, err := s.database.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	return err
}

func (s *Store) MarkRunningJobsInterrupted(ctx context.Context) error {
	now := s.now().UTC().Unix()
	_, err := s.database.ExecContext(ctx, `
		UPDATE jobs
		SET status = 'interrupted', message = '服务重启，任务已中断', updated_at_unix = ?, completed_at_unix = ?
		WHERE status IN ('queued', 'running')
	`, now, now)
	return err
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
		`CREATE TABLE IF NOT EXISTS list_preferences (
			user_id INTEGER NOT NULL,
			list_key TEXT NOT NULL,
			page_size INTEGER NOT NULL DEFAULT 10,
			sort_key TEXT NOT NULL DEFAULT '',
			sort_direction TEXT NOT NULL DEFAULT 'asc',
			updated_at_unix INTEGER NOT NULL,
			PRIMARY KEY (user_id, list_key)
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
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			reference TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			progress INTEGER NOT NULL DEFAULT 0,
			downloaded_bytes INTEGER NOT NULL DEFAULT 0,
			total_bytes INTEGER NOT NULL DEFAULT 0,
			speed_bytes INTEGER NOT NULL DEFAULT 0,
			layers_json TEXT NOT NULL DEFAULT '[]',
			created_at_unix INTEGER NOT NULL,
			updated_at_unix INTEGER NOT NULL,
			completed_at_unix INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS jobs_type_created ON jobs(type, created_at_unix DESC)`,
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
		{name: "source", definition: "TEXT NOT NULL DEFAULT 'edited'"},
		{name: "ignored", definition: "INTEGER NOT NULL DEFAULT 0"},
		{name: "detected_title", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "auto_icon_url", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "local_icon_name", definition: "TEXT NOT NULL DEFAULT ''"},
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
		{name: "navigation_order", definition: `TEXT NOT NULL DEFAULT '["overview","sites","docker","databases","logs","monitoring","system","users","terminal","settings"]'`},
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
