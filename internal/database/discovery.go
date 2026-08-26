package database

import (
	"context"
	"database/sql"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mobycontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func discoverDockerDatabases(ctx context.Context) ([]Source, []sqliteCandidate) {
	apiClient, err := client.New(client.WithHost(localDockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, nil
	}
	defer apiClient.Close()
	response, err := apiClient.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, nil
	}

	sources := make([]Source, 0)
	candidates := make([]sqliteCandidate, 0)
	for _, item := range response.Items {
		inspection, inspectErr := apiClient.ContainerInspect(ctx, item.ID, client.ContainerInspectOptions{})
		if inspectErr != nil || inspection.Container.Config == nil {
			continue
		}
		labels := inspection.Container.Config.Labels
		project := strings.TrimSpace(labels["com.docker.compose.project"])
		service := strings.TrimSpace(labels["com.docker.compose.service"])
		if project == "" {
			project = strings.TrimPrefix(inspection.Container.Name, "/")
		}
		if service == "" {
			service = "数据库服务"
		}

		image := strings.ToLower(inspection.Container.Config.Image)
		if driver, port, ok := databaseImage(image); ok {
			publicPort := publishedDatabasePort(item.Ports, port)
			if publicPort > 0 {
				defaultDatabase := safeEnvironmentValue(inspection.Container.Config.Env, defaultDatabaseKey(driver))
				location := "127.0.0.1:" + strconv.Itoa(publicPort)
				if defaultDatabase != "" {
					location += "/" + defaultDatabase
				}
				sources = append(sources, Source{
					ID: sourceID(driver, project+":"+location), Name: project + " · " + driverName(driver),
					Driver: driver, Category: "project", Project: project, Module: service + " 容器",
					Location: location, Host: "127.0.0.1", Port: publicPort,
					DefaultDatabase: defaultDatabase, RequiresLogin: true, Status: "credentials_required",
					Tags: []string{"容器发现", driverName(driver)},
				})
			}
		}

		for _, environment := range inspection.Container.Config.Env {
			key, value, found := strings.Cut(environment, "=")
			if !found || (!strings.Contains(key, "DATASOURCE_URL") && key != "DATABASE_URL") {
				continue
			}
			if source, ok := sourceFromDatabaseURL(value, project, service); ok {
				sources = append(sources, source)
			}
		}

		for _, mount := range inspection.Container.Mounts {
			destination := strings.ToLower(mount.Destination)
			if mount.Source == "" || (!strings.Contains(destination, "data") &&
				!strings.Contains(destination, "database") && !strings.Contains(destination, "config") &&
				!strings.Contains(destination, "support")) {
				continue
			}
			candidates = append(candidates, sqliteCandidate{
				root: mount.Source, project: project, module: service + " 容器数据",
			})
		}
	}
	return sources, candidates
}

func databaseImage(image string) (Driver, int, bool) {
	switch {
	case strings.Contains(image, "mariadb"), strings.Contains(image, "mysql"):
		return DriverMySQL, 3306, true
	case strings.Contains(image, "postgres"):
		return DriverPostgreSQL, 5432, true
	default:
		return "", 0, false
	}
}

func publishedDatabasePort(ports []mobycontainer.PortSummary, privatePort int) int {
	for _, port := range ports {
		if int(port.PrivatePort) == privatePort && port.PublicPort > 0 {
			return int(port.PublicPort)
		}
	}
	if portOpen(context.Background(), "127.0.0.1:"+strconv.Itoa(privatePort)) {
		return privatePort
	}
	return 0
}

func defaultDatabaseKey(driver Driver) string {
	if driver == DriverPostgreSQL {
		return "POSTGRES_DB"
	}
	return "MYSQL_DATABASE"
}

func safeEnvironmentValue(environment []string, expectedKey string) string {
	for _, item := range environment {
		key, value, found := strings.Cut(item, "=")
		if found && key == expectedKey {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sourceFromDatabaseURL(value, project, service string) (Source, bool) {
	normalized := strings.TrimSpace(strings.TrimPrefix(value, "jdbc:"))
	parsed, err := url.Parse(normalized)
	if err != nil {
		return Source{}, false
	}
	var driver Driver
	var defaultPort int
	switch parsed.Scheme {
	case "mysql", "mariadb":
		driver, defaultPort = DriverMySQL, 3306
	case "postgres", "postgresql":
		driver, defaultPort = DriverPostgreSQL, 5432
	default:
		return Source{}, false
	}
	databaseName := strings.Trim(strings.TrimSpace(parsed.Path), "/")
	if databaseName == "" {
		return Source{}, false
	}
	host := parsed.Hostname()
	if host == "" || host == "localhost" {
		host = "127.0.0.1"
	}
	port := defaultPort
	if parsed.Port() != "" {
		if parsedPort, parseErr := strconv.Atoi(parsed.Port()); parseErr == nil {
			port = parsedPort
		}
	}
	location := host + ":" + strconv.Itoa(port) + "/" + databaseName
	return Source{
		ID: sourceID(driver, project+":"+location), Name: project + " · " + databaseName,
		Driver: driver, Category: "project", Project: project, Module: service + " 使用的数据库",
		Location: location, Host: host, Port: port, DefaultDatabase: databaseName,
		RequiresLogin: true, Status: "credentials_required", Tags: []string{"项目配置发现", driverName(driver)},
	}, true
}

func driverName(driver Driver) string {
	switch driver {
	case DriverMySQL:
		return "MySQL"
	case DriverPostgreSQL:
		return "PostgreSQL"
	default:
		return "SQLite"
	}
}

func portOpen(ctx context.Context, address string) bool {
	dialer := net.Dialer{Timeout: 150 * time.Millisecond}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func isSQLiteFile(ctx context.Context, path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return false
	}
	defer db.Close()
	var count int
	return db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&count) == nil
}

func discoverSQLiteFiles(ctx context.Context, root string) []string {
	result := make([]string, 0)
	visited := 0
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || ctx.Err() != nil {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		depth := len(strings.Split(filepath.ToSlash(relative), "/"))
		if entry.IsDir() {
			name := strings.ToLower(entry.Name())
			if depth > 5 || strings.HasPrefix(name, ".git") || name == "node_modules" ||
				name == ".pnpm-store" || name == ".codex-backups" || name == "#recycle" ||
				name == "backup" || name == "backups" {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if visited > maxScanFiles {
			return fs.SkipAll
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extension != ".db" && extension != ".sqlite" && extension != ".sqlite3" {
			return nil
		}
		if isSQLiteFile(ctx, path) {
			result = append(result, path)
		}
		return nil
	})
	return result
}

func associationForPath(path string) (string, string) {
	normalized := filepath.ToSlash(path)
	for _, prefix := range []string{"/volume2/Project/", "/volume2/DockerProject/"} {
		if strings.HasPrefix(normalized, prefix) {
			remainder := strings.TrimPrefix(normalized, prefix)
			project := strings.SplitN(remainder, "/", 2)[0]
			return project, "项目数据库文件"
		}
	}
	return "未关联项目", "自动发现的 SQLite 数据库"
}
