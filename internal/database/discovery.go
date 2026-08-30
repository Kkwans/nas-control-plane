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
			publicHost, publicPort := publishedDatabaseEndpoint(item.Ports, port)
			reachability, evidence := "host", "published-port"
			if publicPort == 0 && inspection.Container.HostConfig != nil && string(inspection.Container.HostConfig.NetworkMode) == "host" {
				publicHost = "127.0.0.1"
				publicPort = port
				evidence = "host-network"
			}
			if publicPort == 0 {
				reachability = "unreachable"
				evidence = "none"
			}
			defaultDatabase := safeEnvironmentValue(inspection.Container.Config.Env, defaultDatabaseKey(driver))
			location := "容器内部（未发布端口）"
			status := "unreachable"
			host := ""
			if publicPort > 0 {
				host = publicHost
				location = net.JoinHostPort(publicHost, strconv.Itoa(publicPort))
				status = "credentials_required"
			}
			if defaultDatabase != "" {
				location += "/" + defaultDatabase
			}
			sources = append(sources, Source{
				ID: sourceID(driver, project+":"+location), Name: project + " · " + driverName(driver),
				Driver: driver, Category: "project", Project: project, Module: service + " 容器",
				Location: location, Host: host, Port: publicPort,
				DefaultDatabase: defaultDatabase, RequiresLogin: true, Status: status,
				Reachability: reachability, Evidence: evidence,
				Tags: []string{"容器发现", driverName(driver)},
			})
		}

		for _, environment := range inspection.Container.Config.Env {
			key, value, found := strings.Cut(environment, "=")
			if !found || (!strings.Contains(key, "DATASOURCE_URL") && key != "DATABASE_URL") {
				continue
			}
			if source, ok := sourceFromDatabaseURL(value, project, service); ok {
				source = resolveDatabaseURLSource(source, item.Ports, inspection.Container.HostConfig)
				if source.Reachability == "container-internal" || source.Reachability == "unreachable" {
					// A Compose service hostname and its private port are valid only
					// inside that network. Do not expose them as a host endpoint to
					// the Root Agent or render a connect form for a fake address.
					source.Host = ""
					source.Port = 0
					source.Location = "容器内部（未发布端口）"
					if source.DefaultDatabase != "" {
						source.Location += "/" + source.DefaultDatabase
					}
				}
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

func resolveDatabaseURLSource(source Source, ports []mobycontainer.PortSummary, hostConfig *mobycontainer.HostConfig) Source {
	if source.Reachability != "container-internal" {
		return source
	}
	publicHost, publicPort := publishedDatabaseEndpoint(ports, source.Port)
	evidence := "published-port"
	if publicPort == 0 && hostConfig != nil && string(hostConfig.NetworkMode) == "host" {
		publicHost = "127.0.0.1"
		publicPort = source.Port
		evidence = "host-network"
	}
	if publicPort == 0 {
		return source
	}
	databaseName := source.DefaultDatabase
	location := net.JoinHostPort(publicHost, strconv.Itoa(publicPort))
	if databaseName != "" {
		location += "/" + databaseName
	}
	source.Location, source.Host, source.Port = location, publicHost, publicPort
	source.Reachability, source.Evidence, source.Status = "host", evidence, "credentials_required"
	return source
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
	_, port := publishedDatabaseEndpoint(ports, privatePort)
	return port
}

// publishedDatabaseEndpoint returns the host binding that Docker actually
// reports for the selected container port. A wildcard binding is represented
// by the loopback address because the Root Agent connects from the host; a
// specific IPv4 or IPv6 binding must be preserved instead of being rewritten
// to 127.0.0.1.
func publishedDatabaseEndpoint(ports []mobycontainer.PortSummary, privatePort int) (string, int) {
	for _, port := range ports {
		// A database endpoint must be a TCP binding. Docker also reports UDP
		// and SCTP mappings for the same private port; treating those as a
		// database connection would create an endpoint that can never speak the
		// selected driver protocol.
		if int(port.PrivatePort) == privatePort && port.PublicPort > 0 &&
			(strings.TrimSpace(port.Type) == "" || strings.EqualFold(port.Type, "tcp")) {
			host := "127.0.0.1"
			if port.IP.IsValid() && !port.IP.IsUnspecified() {
				host = port.IP.String()
			} else if port.IP.Is6() {
				host = "::1"
			}
			return host, int(port.PublicPort)
		}
	}
	return "", 0
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
	if host == "" {
		return Source{}, false
	}
	// A DATABASE_URL read from a container environment is scoped to that
	// container's network namespace. Resolve it to a host endpoint only after
	// inspecting a published binding or host-network mode below.
	reachability := "container-internal"
	evidence := "database-url"
	status := "unreachable"
	host = strings.TrimSpace(host)
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
		RequiresLogin: true, Status: status, Reachability: reachability, Evidence: evidence,
		Tags: []string{"项目配置发现", driverName(driver)},
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
