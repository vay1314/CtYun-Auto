package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }
type Account struct {
	ID                                                                            int64
	Name, Username, PasswordEncrypted, DeviceCode, ChatCron, PCCron, DeviceStatus string
	Enabled, ChatEnabled, PCEnabled                                               bool
}
type Run struct {
	ID, AccountID                                                                   int64
	AccountName, TaskType, Trigger, Status, StartedAt, FinishedAt, LogPath, Message string
}
type PlatformStatus struct {
	AccountID        int64
	TotalPoints      *int
	Tasks            map[string]TaskStatus
	UpdatedAt, Error string
}
type TaskStatus struct {
	State      string `json:"state"`
	StateLabel string `json:"state_label"`
	Current    int    `json:"current"`
	Total      int    `json:"total"`
}
type RedeemConfig struct {
	AccountID                                      int64
	Enabled                                        bool
	ProductID, ProductName, ProductType, DesktopID string
	CostPoints, MaxTimes                           int
	ScheduleType                                   string
	IntervalDays                                   int
	MonthlyDays                                    string
	UpdatedAt                                      string
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db}
	if err = s.Init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func Now() string             { return time.Now().Format(time.RFC3339) }
func (s *Store) Init() error {
	_, err := s.DB.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=30000;
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS accounts(id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT NOT NULL,username TEXT NOT NULL UNIQUE,password_encrypted TEXT NOT NULL,device_code TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 1,chat_enabled INTEGER NOT NULL DEFAULT 1,chat_cron TEXT NOT NULL DEFAULT '0 3,20 * * *',pc_enabled INTEGER NOT NULL DEFAULT 1,pc_cron TEXT NOT NULL DEFAULT '0 4,6 * * *',device_status TEXT NOT NULL DEFAULT 'unknown',created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS task_runs(id INTEGER PRIMARY KEY AUTOINCREMENT,account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,task_type TEXT NOT NULL,trigger_source TEXT NOT NULL,status TEXT NOT NULL,started_at TEXT NOT NULL,finished_at TEXT,exit_code INTEGER,log_path TEXT NOT NULL,message TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS scheduler_claims(account_id INTEGER NOT NULL,task_type TEXT NOT NULL,minute_key TEXT NOT NULL,PRIMARY KEY(account_id,task_type,minute_key));
CREATE TABLE IF NOT EXISTS account_platform_status(account_id INTEGER PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,total_points INTEGER,tasks_json TEXT NOT NULL DEFAULT '{}',updated_at TEXT NOT NULL,error TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS account_auth_cache(account_id INTEGER PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,login_info_encrypted TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS account_native_auth_cache(account_id INTEGER PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,login_info_encrypted TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS redeem_configs(account_id INTEGER PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,enabled INTEGER NOT NULL DEFAULT 0,product_id TEXT NOT NULL DEFAULT '',product_name TEXT NOT NULL DEFAULT '',product_type TEXT NOT NULL DEFAULT '',desktop_id TEXT NOT NULL DEFAULT '',cost_points INTEGER NOT NULL DEFAULT 0,max_times INTEGER NOT NULL DEFAULT 1,schedule_type TEXT NOT NULL DEFAULT 'daily',interval_days INTEGER NOT NULL DEFAULT 1,monthly_days TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS redeem_states(account_id INTEGER PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,last_attempt_date TEXT NOT NULL DEFAULT '',last_attempt_status TEXT NOT NULL DEFAULT '',last_success_date TEXT NOT NULL DEFAULT '',last_redeem_times INTEGER NOT NULL DEFAULT 0,last_points_spent INTEGER NOT NULL DEFAULT 0,message TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_task_runs_started_at ON task_runs(started_at DESC);`)
	if err == nil {
		_, err = s.DB.Exec("UPDATE task_runs SET status='interrupted',finished_at=?,message='服务重启，任务状态已重置' WHERE status IN ('queued','running')", Now())
	}
	return err
}
func (s *Store) Setting(k string) (string, error) {
	var v string
	err := s.DB.QueryRow("SELECT value FROM settings WHERE key=?", k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
func (s *Store) SetSetting(k, v string) error {
	_, e := s.DB.Exec("INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at", k, v, Now())
	return e
}
func scanAccount(r interface{ Scan(...any) error }) (Account, error) {
	var a Account
	var en, ch, pc int
	e := r.Scan(&a.ID, &a.Name, &a.Username, &a.PasswordEncrypted, &a.DeviceCode, &en, &ch, &a.ChatCron, &pc, &a.PCCron, &a.DeviceStatus)
	a.Enabled = en != 0
	a.ChatEnabled = ch != 0
	a.PCEnabled = pc != 0
	return a, e
}

const accountCols = "id,name,username,password_encrypted,device_code,enabled,chat_enabled,chat_cron,pc_enabled,pc_cron,device_status"

func (s *Store) Accounts() ([]Account, error) {
	rows, e := s.DB.Query("SELECT " + accountCols + " FROM accounts ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, e := scanAccount(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) Account(id int64) (Account, error) {
	return scanAccount(s.DB.QueryRow("SELECT "+accountCols+" FROM accounts WHERE id=?", id))
}
func (s *Store) SaveAccount(a Account, password string, key []byte, encrypt func(string, []byte) (string, error)) (int64, error) {
	now := Now()
	if a.Name == "" || a.Username == "" || a.DeviceCode == "" {
		return 0, errors.New("名称、账号和设备码不能为空")
	}
	if a.ID == 0 {
		if password == "" {
			return 0, errors.New("密码不能为空")
		}
		enc, e := encrypt(password, key)
		if e != nil {
			return 0, e
		}
		r, e := s.DB.Exec(`INSERT INTO accounts(name,username,password_encrypted,device_code,enabled,chat_enabled,chat_cron,pc_enabled,pc_cron,device_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'unknown',?,?)`, a.Name, a.Username, enc, a.DeviceCode, a.Enabled, a.ChatEnabled, a.ChatCron, a.PCEnabled, a.PCCron, now, now)
		if e != nil {
			return 0, e
		}
		return r.LastInsertId()
	}
	old, e := s.Account(a.ID)
	if e != nil {
		return 0, e
	}
	enc := old.PasswordEncrypted
	if password != "" {
		enc, e = encrypt(password, key)
		if e != nil {
			return 0, e
		}
	}
	_, e = s.DB.Exec(`UPDATE accounts SET name=?,username=?,password_encrypted=?,device_code=?,enabled=?,chat_enabled=?,chat_cron=?,pc_enabled=?,pc_cron=?,device_status=CASE WHEN username<>? OR device_code<>? THEN 'unknown' ELSE device_status END,updated_at=? WHERE id=?`, a.Name, a.Username, enc, a.DeviceCode, a.Enabled, a.ChatEnabled, a.ChatCron, a.PCEnabled, a.PCCron, a.Username, a.DeviceCode, now, a.ID)
	return a.ID, e
}
func (s *Store) DeleteAccount(id int64) error {
	_, e := s.DB.Exec("DELETE FROM accounts WHERE id=?", id)
	return e
}
func (s *Store) SetDeviceStatus(id int64, status string) error {
	_, e := s.DB.Exec("UPDATE accounts SET device_status=?,updated_at=? WHERE id=?", status, Now(), id)
	return e
}
func (s *Store) AuthCache(id int64) (string, error) {
	var v string
	e := s.DB.QueryRow("SELECT login_info_encrypted FROM account_auth_cache WHERE account_id=?", id).Scan(&v)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return v, e
}
func (s *Store) SaveAuthCache(id int64, value string) error {
	_, e := s.DB.Exec(`INSERT INTO account_auth_cache(account_id,login_info_encrypted,updated_at) VALUES(?,?,?) ON CONFLICT(account_id) DO UPDATE SET login_info_encrypted=excluded.login_info_encrypted,updated_at=excluded.updated_at`, id, value, Now())
	return e
}
func (s *Store) ClearAuthCache(id int64) {
	_, _ = s.DB.Exec("DELETE FROM account_auth_cache WHERE account_id=?", id)
}
func (s *Store) NativeAuthCache(id int64) (string, error) {
	var v string
	e := s.DB.QueryRow("SELECT login_info_encrypted FROM account_native_auth_cache WHERE account_id=?", id).Scan(&v)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return v, e
}
func (s *Store) SaveNativeAuthCache(id int64, value string) error {
	_, e := s.DB.Exec(`INSERT INTO account_native_auth_cache(account_id,login_info_encrypted,updated_at) VALUES(?,?,?) ON CONFLICT(account_id) DO UPDATE SET login_info_encrypted=excluded.login_info_encrypted,updated_at=excluded.updated_at`, id, value, Now())
	return e
}
func (s *Store) ClearNativeAuthCache(id int64) {
	_, _ = s.DB.Exec("DELETE FROM account_native_auth_cache WHERE account_id=?", id)
}
func (s *Store) AddRun(accountID int64, typ, trigger, logPath string) (int64, error) {
	r, e := s.DB.Exec("INSERT INTO task_runs(account_id,task_type,trigger_source,status,started_at,log_path) VALUES(?,?,?,'queued',?,?)", accountID, typ, trigger, Now(), logPath)
	if e != nil {
		return 0, e
	}
	return r.LastInsertId()
}
func (s *Store) UpdateRun(id int64, status, message string) error {
	finish := any(nil)
	if status != "queued" && status != "running" {
		finish = Now()
	}
	_, e := s.DB.Exec("UPDATE task_runs SET status=?,message=?,finished_at=COALESCE(?,finished_at) WHERE id=?", status, message, finish, id)
	return e
}
func (s *Store) Runs(limit int) ([]Run, error) {
	rows, e := s.DB.Query(`SELECT r.id,COALESCE(r.account_id,0),COALESCE(a.name,''),r.task_type,r.trigger_source,r.status,r.started_at,COALESCE(r.finished_at,''),r.log_path,r.message FROM task_runs r LEFT JOIN accounts a ON a.id=r.account_id ORDER BY r.id DESC LIMIT ?`, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var v Run
		if e = rows.Scan(&v.ID, &v.AccountID, &v.AccountName, &v.TaskType, &v.Trigger, &v.Status, &v.StartedAt, &v.FinishedAt, &v.LogPath, &v.Message); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) Run(id int64) (Run, error) {
	var v Run
	e := s.DB.QueryRow(`SELECT r.id,COALESCE(r.account_id,0),COALESCE(a.name,''),r.task_type,r.trigger_source,r.status,r.started_at,COALESCE(r.finished_at,''),r.log_path,r.message FROM task_runs r LEFT JOIN accounts a ON a.id=r.account_id WHERE r.id=?`, id).Scan(&v.ID, &v.AccountID, &v.AccountName, &v.TaskType, &v.Trigger, &v.Status, &v.StartedAt, &v.FinishedAt, &v.LogPath, &v.Message)
	return v, e
}

func (s *Store) RunLogs() ([]Run, error) {
	rows, e := s.DB.Query(`SELECT id,status,log_path FROM task_runs ORDER BY id DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var v Run
		if e = rows.Scan(&v.ID, &v.Status, &v.LogPath); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) PurgeCompletedRunsBefore(cutoff string) ([]string, error) {
	tx, e := s.DB.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	rows, e := tx.Query(`SELECT log_path FROM task_runs WHERE status NOT IN ('queued','running') AND COALESCE(NULLIF(finished_at,''),started_at) < ?`, cutoff)
	if e != nil {
		return nil, e
	}
	var paths []string
	for rows.Next() {
		var path string
		if e = rows.Scan(&path); e != nil {
			rows.Close()
			return nil, e
		}
		paths = append(paths, path)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec(`DELETE FROM task_runs WHERE status NOT IN ('queued','running') AND COALESCE(NULLIF(finished_at,''),started_at) < ?`, cutoff); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return paths, nil
}
func (s *Store) SavePlatform(v PlatformStatus) error {
	raw, _ := json.Marshal(v.Tasks)
	points := any(nil)
	if v.TotalPoints != nil {
		points = *v.TotalPoints
	}
	_, e := s.DB.Exec(`INSERT INTO account_platform_status(account_id,total_points,tasks_json,updated_at,error) VALUES(?,?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET total_points=excluded.total_points,tasks_json=excluded.tasks_json,updated_at=excluded.updated_at,error=excluded.error`, v.AccountID, points, string(raw), Now(), v.Error)
	return e
}
func (s *Store) Platforms() (map[int64]PlatformStatus, error) {
	rows, e := s.DB.Query("SELECT account_id,total_points,tasks_json,updated_at,error FROM account_platform_status")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[int64]PlatformStatus{}
	for rows.Next() {
		var v PlatformStatus
		var p sql.NullInt64
		var raw string
		if e = rows.Scan(&v.AccountID, &p, &raw, &v.UpdatedAt, &v.Error); e != nil {
			return nil, e
		}
		if p.Valid {
			x := int(p.Int64)
			v.TotalPoints = &x
		}
		_ = json.Unmarshal([]byte(raw), &v.Tasks)
		out[v.AccountID] = v
	}
	return out, rows.Err()
}
func (s *Store) Claim(id int64, typ, minute string) bool {
	r, e := s.DB.Exec("INSERT OR IGNORE INTO scheduler_claims(account_id,task_type,minute_key) VALUES(?,?,?)", id, typ, minute)
	if e != nil {
		return false
	}
	n, _ := r.RowsAffected()
	return n == 1
}
func (s *Store) Redeem(id int64) (RedeemConfig, error) {
	var v RedeemConfig
	var en int
	e := s.DB.QueryRow(`SELECT account_id,enabled,product_id,product_name,product_type,desktop_id,cost_points,max_times,schedule_type,interval_days,monthly_days,updated_at FROM redeem_configs WHERE account_id=?`, id).Scan(&v.AccountID, &en, &v.ProductID, &v.ProductName, &v.ProductType, &v.DesktopID, &v.CostPoints, &v.MaxTimes, &v.ScheduleType, &v.IntervalDays, &v.MonthlyDays, &v.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		v.AccountID = id
		v.ScheduleType = "daily"
		v.IntervalDays = 1
		v.MaxTimes = 1
		return v, nil
	}
	v.Enabled = en != 0
	return v, e
}
func (s *Store) SaveRedeem(v RedeemConfig) error {
	if v.MaxTimes < 1 {
		v.MaxTimes = 1
	}
	if v.IntervalDays < 1 {
		v.IntervalDays = 1
	}
	_, e := s.DB.Exec(`INSERT INTO redeem_configs(account_id,enabled,product_id,product_name,product_type,desktop_id,cost_points,max_times,schedule_type,interval_days,monthly_days,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET enabled=excluded.enabled,product_id=excluded.product_id,product_name=excluded.product_name,product_type=excluded.product_type,desktop_id=excluded.desktop_id,cost_points=excluded.cost_points,max_times=excluded.max_times,schedule_type=excluded.schedule_type,interval_days=excluded.interval_days,monthly_days=excluded.monthly_days,updated_at=excluded.updated_at`, v.AccountID, v.Enabled, v.ProductID, v.ProductName, v.ProductType, v.DesktopID, v.CostPoints, v.MaxTimes, v.ScheduleType, v.IntervalDays, v.MonthlyDays, Now())
	return e
}
func (s *Store) Debug() string { return fmt.Sprintf("%p", s.DB) }
