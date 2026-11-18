package api

import (
	"net/http"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Backup API Handlers
//
// The server holds the database files exclusively, so it is the only process
// that can copy them while the notes stay in use. These two endpoints are the
// on-demand door to what models.StartBackupSchedule does on a timer.
//
// Admin only. A backup contains every user's notes and the users table, and
// the list reveals where on the server's disk that copy sits; neither is an
// ordinary account's business on a shared hub.
// ============================================================================

// backupListResponse is the payload of GET /api/v1/admin/backups.
type backupListResponse struct {
	Dir             string             `json:"dir"`
	IntervalSeconds float64            `json:"interval_seconds"` // 0 when the schedule is off
	Keep            int                `json:"keep"`
	Backups         []models.BackupSet `json:"backups"` // newest first
}

// BackupNow handles POST /api/v1/admin/backup
// Takes a backup of both databases now and applies rotation. Answers with the
// set that represents the current data; `unchanged` is true when that is the
// existing newest set because nothing has changed since it was taken.
func BackupNow(ctx rweb.Context) error {
	if !IsAdmin(ctx) {
		return writeError(ctx, http.StatusForbidden, "admin access required")
	}

	res, err := models.BackupDatabases(models.LoadBackupConfig())
	if err != nil {
		logger.LogErr(serr.Wrap(err, "on-demand backup failed"))
		return writeError(ctx, http.StatusInternalServerError, "failed to back up the databases")
	}
	return writeSuccess(ctx, http.StatusOK, res)
}

// ListBackups handles GET /api/v1/admin/backups
// Lists the existing backup sets, newest first, with the settings in force.
func ListBackups(ctx rweb.Context) error {
	if !IsAdmin(ctx) {
		return writeError(ctx, http.StatusForbidden, "admin access required")
	}

	cfg := models.LoadBackupConfig()
	sets, err := models.ListBackups(cfg.Dir)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "listing backups failed"))
		return writeError(ctx, http.StatusInternalServerError, "failed to list backups")
	}
	if sets == nil {
		sets = []models.BackupSet{} // an empty JSON array, not null
	}
	return writeSuccess(ctx, http.StatusOK, backupListResponse{
		Dir:             cfg.Dir,
		IntervalSeconds: cfg.Interval.Seconds(),
		Keep:            cfg.Keep,
		Backups:         sets,
	})
}
