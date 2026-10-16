package controlstore

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"
)

var (
	ErrJobExists          = errors.New("job already exists")
	ErrJobNotFound        = errors.New("job not found")
	ErrJobConflict        = errors.New("job revision conflict")
	ErrJobInvalidRevision = errors.New("invalid job revision")
)

const jobColumns = `id, type, status, reference, message, error, progress,
 downloaded_bytes, total_bytes, speed_bytes, layers_json,
 created_at_unix, updated_at_unix, completed_at_unix,
 revision, cancellation_status, error_code, progress_limited`

// 创建结果先按字节数投影，损坏或超限数据不会被完整加载到 Go。
const jobReadColumns = jobColumns + `,
 CASE WHEN typeof(create_result_json) = 'text' AND length(CAST(create_result_json AS BLOB)) <= 256
 THEN create_result_json ELSE '!' END`

func jobCompletedUnix(job JobRecord) int64 {
	if job.CompletedAt.IsZero() {
		return 0
	}
	return job.CompletedAt.UTC().Unix()
}

// CreateJob 仅创建新记录；初始版本固定为 1，不使用调用方提供的 Revision。
func (s *Store) CreateJob(ctx context.Context, job JobRecord) error {
	if _, err := ParseContainerCreateJobResult(job); err != nil {
		return err
	}
	result, err := s.database.ExecContext(ctx, `INSERT INTO jobs (`+jobColumns+`, create_result_json)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)
 ON CONFLICT(id) DO NOTHING`, job.ID, job.Type, job.Status, job.Reference,
		job.Message, job.Error, job.Progress, job.DownloadedBytes, job.TotalBytes,
		job.SpeedBytes, job.LayersJSON, job.CreatedAt.UTC().Unix(), job.UpdatedAt.UTC().Unix(),
		jobCompletedUnix(job), job.CancellationStatus, job.ErrorCode, job.ProgressLimited, job.CreateResultJSON)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrJobExists
	}
	return nil
}

// UpdateJob 只更新匹配版本的现有记录，永不插入；冲突后不得用旧快照直接重试。
// ID、类型、引用及创建时间不可变；expectedRevision 是唯一的版本比较依据。
func (s *Store) UpdateJob(ctx context.Context, job JobRecord, expectedRevision int64) (int64, error) {
	if expectedRevision < 1 || expectedRevision == math.MaxInt64 {
		return 0, ErrJobInvalidRevision
	}
	// 类型不可变，不能使用调用方改写的类型绕过已保存记录的结果约束。
	if job.Status == "completed" || job.CreateResultJSON != "" {
		if err := s.database.QueryRowContext(ctx, `SELECT type FROM jobs WHERE id = ?`, job.ID).Scan(&job.Type); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return 0, ErrJobNotFound
			}
			return 0, err
		}
	}
	if _, err := ParseContainerCreateJobResult(job); err != nil {
		return 0, err
	}
	err := s.mutateJobRevision(ctx, job.ID, expectedRevision, `UPDATE jobs SET
 status = ?, message = ?, error = ?, progress = ?, downloaded_bytes = ?,
 total_bytes = ?, speed_bytes = ?, layers_json = ?, updated_at_unix = ?,
 completed_at_unix = ?, cancellation_status = ?, error_code = ?, progress_limited = ?, create_result_json = ?, revision = revision + 1
 WHERE id = ? AND revision = ?`, job.Status, job.Message, job.Error, job.Progress,
		job.DownloadedBytes, job.TotalBytes, job.SpeedBytes, job.LayersJSON, job.UpdatedAt.UTC().Unix(),
		jobCompletedUnix(job), job.CancellationStatus, job.ErrorCode, job.ProgressLimited, job.CreateResultJSON)
	if err != nil {
		return 0, err
	}
	return expectedRevision + 1, nil
}

// DeleteJobRevision 以版本为条件删除；缺失与冲突分别返回稳定错误。
func (s *Store) DeleteJobRevision(ctx context.Context, id string, expectedRevision int64) error {
	if expectedRevision < 1 {
		return ErrJobInvalidRevision
	}
	return s.mutateJobRevision(ctx, id, expectedRevision, `DELETE FROM jobs WHERE id = ? AND revision = ?`)
}

// 条件写及缺失检查在同一事务内完成，避免并发删除改变冲突分类。
func (s *Store) mutateJobRevision(ctx context.Context, id string, revision int64, query string, args ...any) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	args = append(args, id, revision)
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		var existing int64
		err := tx.QueryRowContext(ctx, `SELECT revision FROM jobs WHERE id = ?`, id).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrJobNotFound
		}
		if err != nil {
			return err
		}
		return ErrJobConflict
	}
	return tx.Commit()
}

func scanJob(row interface{ Scan(...any) error }) (JobRecord, error) {
	var job JobRecord
	var created, updated, completed int64
	err := row.Scan(&job.ID, &job.Type, &job.Status, &job.Reference, &job.Message, &job.Error,
		&job.Progress, &job.DownloadedBytes, &job.TotalBytes, &job.SpeedBytes, &job.LayersJSON,
		&created, &updated, &completed, &job.Revision, &job.CancellationStatus, &job.ErrorCode, &job.ProgressLimited, &job.CreateResultJSON)
	if err != nil {
		return JobRecord{}, err
	}
	job.CreatedAt = time.Unix(created, 0).UTC()
	job.UpdatedAt = time.Unix(updated, 0).UTC()
	if completed > 0 {
		job.CompletedAt = time.Unix(completed, 0).UTC()
	}
	return job, nil
}

func (s *Store) Job(ctx context.Context, id string) (JobRecord, error) {
	job, err := scanJob(s.database.QueryRowContext(ctx, `SELECT `+jobReadColumns+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return JobRecord{}, ErrJobNotFound
	}
	return job, err
}

const recentJobsQuery = `SELECT ` + jobReadColumns + ` FROM jobs ORDER BY created_at_unix DESC, id DESC LIMIT ?`
const recentJobsByTypeQuery = `SELECT ` + jobReadColumns + ` FROM jobs WHERE type = ? ORDER BY created_at_unix DESC, id DESC LIMIT ?`

func (s *Store) Jobs(ctx context.Context, kind string, limit int) ([]JobRecord, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	// 分开固定查询，让混合历史和指定类型都能按索引顺序取得有界前缀。
	// 不使用可选条件 OR，避免为了最近几条记录扫描、排序全部历史。
	query, args := recentJobsQuery, []any{limit}
	if kind != "" {
		query, args = recentJobsByTypeQuery, []any{kind, limit}
	}
	rows, err := s.database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]JobRecord, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, job)
	}
	return result, rows.Err()
}

// RecoverInterruptedJobs 仅由单实例所有者在启动期间调用；不保证远端操作已停止。
// 单条 UPDATE 是原子的；恢复也推进版本，拒绝恢复前生成的旧快照。
func (s *Store) RecoverInterruptedJobs(ctx context.Context) error {
	now := s.now().UTC().Unix()
	_, err := s.database.ExecContext(ctx, `UPDATE jobs SET status = 'interrupted',
 message = '控制面重启，执行结果未确认', speed_bytes = 0, create_result_json = '',
 cancellation_status = CASE WHEN cancellation_status = 'requested' THEN 'unknown' ELSE cancellation_status END,
 updated_at_unix = ?, completed_at_unix = ?, revision = revision + 1
 WHERE status IN ('queued', 'running')`, now, now)
	return err
}

// MarkRunningJobsInterrupted 暂时保留旧调用接口，后续由 engine 显式恢复替代。
func (s *Store) MarkRunningJobsInterrupted(ctx context.Context) error {
	return s.RecoverInterruptedJobs(ctx)
}

// DeleteJob 暂时保留旧 HTTP 接口；不具备版本保护，新 engine 必须用 DeleteJobRevision。
func (s *Store) DeleteJob(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("job id is required")
	}
	_, err := s.database.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	return err
}

// UpsertJob 仅供旧 HTTP 调用过渡使用，会重建缺失行；新 engine 禁止调用。
func (s *Store) UpsertJob(ctx context.Context, job JobRecord) error {
	if job.Status == "completed" || job.CreateResultJSON != "" {
		var kind string
		err := s.database.QueryRowContext(ctx, `SELECT type FROM jobs WHERE id = ?`, job.ID).Scan(&kind)
		if err == nil {
			job.Type = kind
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if _, err := ParseContainerCreateJobResult(job); err != nil {
		return err
	}
	completedAt := int64(0)
	if !job.CompletedAt.IsZero() {
		completedAt = job.CompletedAt.UTC().Unix()
	}
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO jobs (
			id, type, status, reference, message, error, progress,
			downloaded_bytes, total_bytes, speed_bytes, layers_json,
			created_at_unix, updated_at_unix, completed_at_unix, progress_limited, create_result_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			completed_at_unix = excluded.completed_at_unix,
			progress_limited = excluded.progress_limited,
			create_result_json = excluded.create_result_json,
			revision = jobs.revision + 1
	`, job.ID, job.Type, job.Status, job.Reference, job.Message, job.Error, job.Progress,
		job.DownloadedBytes, job.TotalBytes, job.SpeedBytes, job.LayersJSON,
		job.CreatedAt.UTC().Unix(), job.UpdatedAt.UTC().Unix(), completedAt, job.ProgressLimited, job.CreateResultJSON)
	return err
}
