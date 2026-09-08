package web

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/vay1314/CtYun-Keeper/internal/ctyun"
	"github.com/vay1314/CtYun-Keeper/internal/security"
	"github.com/vay1314/CtYun-Keeper/internal/service"
	"github.com/vay1314/CtYun-Keeper/internal/storage"
)

type Server struct {
	store                     *storage.Store
	manager                   *service.Manager
	sessionKey, credentialKey []byte
	version, dataDir          string
	secure                    bool
	mux                       *http.ServeMux
}

func New(store *storage.Store, m *service.Manager, sessionKey, credentialKey []byte, version, dataDir, staticDir string, secure bool) *Server {
	s := &Server{store: store, manager: m, sessionKey: sessionKey, credentialKey: credentialKey, version: version, dataDir: dataDir, secure: secure, mux: http.NewServeMux()}
	s.routes(staticDir)
	return s
}
func (s *Server) Handler() http.Handler { return s.securityHeaders(s.mux) }
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; font-src 'self'")
		next.ServeHTTP(w, r)
	})
}
func (s *Server) routes(staticDir string) {
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
	s.mux.HandleFunc("/health", s.health)
	s.mux.HandleFunc("/setup", s.setup)
	s.mux.HandleFunc("/login", s.login)
	s.mux.HandleFunc("/logout", s.logout)
	s.mux.HandleFunc("/accounts/save", s.saveAccount)
	s.mux.HandleFunc("/accounts/new", s.accountNew)
	s.mux.HandleFunc("/accounts/", s.accountRoute)
	s.mux.HandleFunc("/accounts", s.accounts)
	s.mux.HandleFunc("/partials/status", s.statusPartial)
	s.mux.HandleFunc("/partials/accounts", s.accountsPartial)
	s.mux.HandleFunc("/partials/log-sources", s.logSourcesPartial)
	s.mux.HandleFunc("/partials/task-accounts", s.taskCards)
	s.mux.HandleFunc("/tasks/", s.taskRoute)
	s.mux.HandleFunc("/tasks", s.tasks)
	s.mux.HandleFunc("/logs/clear", s.clearLog)
	s.mux.HandleFunc("/logs", s.logs)
	s.mux.HandleFunc("/logs/", s.logStream)
	s.mux.HandleFunc("/ctyun/restart", s.restart)
	s.mux.HandleFunc("/settings/password", s.password)
	s.mux.HandleFunc("/settings/logs/clear", s.clearAllLogs)
	s.mux.HandleFunc("/settings/logs", s.logSettings)
	s.mux.HandleFunc("/settings", s.settings)
	s.mux.HandleFunc("/api/status", s.apiStatus)
	s.mux.HandleFunc("/api/accounts/", s.apiAccountRoute)
	s.mux.HandleFunc("/", s.dashboard)
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","version":%q}`, s.version)
}
func (s *Server) cookie(w http.ResponseWriter, name, value string, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: 12 * 3600, HttpOnly: httpOnly, Secure: s.secure, SameSite: http.SameSiteStrictMode})
}
func (s *Server) csrf(w http.ResponseWriter, r *http.Request) string {
	if c, e := r.Cookie("ctyun_csrf"); e == nil && security.VerifyCookie(s.sessionKey, c.Value) {
		return strings.Split(c.Value, ".")[0]
	}
	v := security.RandomToken(24)
	s.cookie(w, "ctyun_csrf", security.SignCookie(s.sessionKey, v), true)
	return v
}
func (s *Server) checkCSRF(r *http.Request) bool {
	c, e := r.Cookie("ctyun_csrf")
	if e != nil || !security.VerifyCookie(s.sessionKey, c.Value) {
		return false
	}
	expected := strings.Split(c.Value, ".")[0]
	return r.FormValue("csrf_token") == expected || r.Header.Get("X-CSRF-Token") == expected
}
func (s *Server) authed(r *http.Request) bool {
	c, e := r.Cookie("ctyun_session")
	return e == nil && security.VerifyCookie(s.sessionKey, c.Value) && strings.HasPrefix(c.Value, "authenticated.")
}
func (s *Server) guard(w http.ResponseWriter, r *http.Request) bool {
	hash, _ := s.store.Setting("admin_password_hash")
	if hash == "" {
		http.Redirect(w, r, "/setup", 303)
		return false
	}
	if !s.authed(r) {
		http.Redirect(w, r, "/login", 303)
		return false
	}
	return true
}
func esc(v string) string { return html.EscapeString(v) }
func formatTime(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "尚未更新"
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, v); err == nil {
			return parsed.Format("2006-01-02 15:04:05")
		}
	}
	if len(v) >= 19 && v[10] == 'T' {
		return v[:10] + " " + v[11:19]
	}
	return v
}
func platformStatusUpdatedToday(updatedAt string, now time.Time) bool {
	updatedAt = strings.TrimSpace(updatedAt)
	if updatedAt == "" {
		return false
	}
	var updated time.Time
	var err error
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		updated, err = time.Parse(layout, updatedAt)
		if err == nil {
			break
		}
	}
	if err != nil {
		updated, err = time.ParseInLocation("2006-01-02 15:04:05", updatedAt, now.Location())
	}
	if err != nil {
		return false
	}
	updated = updated.In(now.Location())
	year, month, day := now.Date()
	updatedYear, updatedMonth, updatedDay := updated.Date()
	return year == updatedYear && month == updatedMonth && day == updatedDay
}
func reverseLogText(v string) string {
	v = strings.ReplaceAll(v, "\r\n", "\n")
	if v == "" {
		return ""
	}
	trailingNewline := strings.HasSuffix(v, "\n")
	v = strings.TrimSuffix(v, "\n")
	lines := strings.Split(v, "\n")
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	result := strings.Join(lines, "\n")
	if trailingNewline {
		result += "\n"
	}
	return result
}
func taskLabel(v string) string {
	return map[string]string{"login": "登录云电脑", "chat": "AI 对话", "pc": "云电脑挂机", "redeem": "自动兑换"}[v]
}
func statusLabel(v string) string {
	if label := map[string]string{"queued": "等待中", "running": "运行中", "success": "已完成", "failed": "失败", "stopped": "已停止", "interrupted": "已中断"}[v]; label != "" {
		return label
	}
	return v
}
func buttonIcon(name string) string {
	path := map[string]string{
		"login":  `<path d="M10 17l5-5-5-5"/><path d="M15 12H3"/><path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"/>`,
		"usage":  `<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>`,
		"chat":   `<path d="M21 15a4 4 0 0 1-4 4H8l-5 3V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4z"/><path d="M8 9h8M8 13h5"/>`,
		"status": `<rect x="4" y="3" width="16" height="18" rx="2"/><path d="M8 8h8M8 12h5M8 16h8"/>`,
	}[name]
	return `<svg class="button-icon" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + path + `</svg>`
}
func dashboardIcon(name string) string {
	path := map[string]string{
		"cloud":   `<path d="M7 18h10a4 4 0 0 0 .7-7.94A6 6 0 0 0 6.26 8.1 4.5 4.5 0 0 0 7 18Z"/><path d="m9.5 13 1.7 1.7 3.5-3.7"/>`,
		"users":   `<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"/>`,
		"tasks":   `<path d="m13 2-9 12h8l-1 8 9-12h-8l1-8Z"/>`,
		"history": `<path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5M12 7v5l3 2"/>`,
		"server":  `<rect x="3" y="4" width="18" height="6" rx="2"/><rect x="3" y="14" width="18" height="6" rx="2"/><path d="M7 7h.01M7 17h.01"/>`,
		"uptime":  `<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>`,
		"cpu":     `<rect x="7" y="7" width="10" height="10" rx="1"/><path d="M9 1v3M15 1v3M9 20v3M15 20v3M20 9h3M20 14h3M1 9h3M1 14h3"/>`,
		"version": `<path d="m12 3 8 4.5v9L12 21l-8-4.5v-9L12 3Z"/><path d="m4.5 7.5 7.5 4 7.5-4M12 21v-9.5"/>`,
	}[name]
	return `<svg class="dashboard-icon" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + path + `</svg>`
}
func dashboardTaskLabel(v string) string {
	if label := map[string]string{"login": "登陆任务", "pc": "时长任务", "chat": "AI对话任务", "redeem": "自动兑换任务"}[v]; label != "" {
		return label
	}
	return "其他任务"
}
func passwordToggle() string {
	return `<button class="password-toggle" type="button" data-password-toggle aria-label="显示密码" aria-pressed="false"><svg class="eye-open" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/></svg><svg class="eye-closed" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m3 3 18 18"/><path d="M10.6 6.2A10.9 10.9 0 0 1 12 6c6.5 0 10 6 10 6a18.5 18.5 0 0 1-2.1 2.8M6.6 6.6C3.6 8.4 2 12 2 12s3.5 6 10 6c1.8 0 3.3-.5 4.6-1.2"/></svg></button>`
}
func navActive(path, target string) string {
	if (target == "/" && path == "/") || (target != "/" && strings.HasPrefix(path, target)) {
		return "active"
	}
	return ""
}
func checked(v bool) string {
	if v {
		return " checked"
	}
	return ""
}
func disabled(v bool) string {
	if v {
		return " disabled"
	}
	return ""
}
func selected(v bool) string {
	if v {
		return " selected"
	}
	return ""
}
func (s *Server) page(w http.ResponseWriter, r *http.Request, title, content string, auth bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	token := s.csrf(w, r)
	content = strings.ReplaceAll(content, "{{CSRF}}", esc(token))
	notice := r.URL.Query().Get("notice")
	flash := ""
	if notice != "" {
		flash = `<div class="flash success" role="status">` + esc(notice) + `</div>`
	}
	if errText := r.URL.Query().Get("error"); errText != "" {
		flash = `<div class="flash error" role="alert">` + esc(errText) + `</div>`
	}
	nav := ""
	mainClass := "auth-shell"
	if auth {
		mainClass = "main-shell"
		nav = `<aside class="sidebar" id="sidebar"><a class="brand" href="/"><span class="brand-mark material-symbols-rounded">cloud_sync</span><span><strong>CtYunKeeper</strong><small>云电脑管理台</small></span></a><nav><span class="nav-section">管理</span><a class="` + navActive(r.URL.Path, "/") + `" href="/"><span class="material-symbols-rounded">dashboard</span><span>仪表盘</span></a><a class="` + navActive(r.URL.Path, "/accounts") + `" href="/accounts"><span class="material-symbols-rounded">manage_accounts</span><span>账号管理</span></a><a class="` + navActive(r.URL.Path, "/tasks") + `" href="/tasks"><span class="material-symbols-rounded">schedule</span><span>任务中心</span></a><span class="nav-section">系统</span><a class="` + navActive(r.URL.Path, "/logs") + `" href="/logs"><span class="material-symbols-rounded">terminal</span><span>日志中心</span></a><a class="` + navActive(r.URL.Path, "/settings") + `" href="/settings"><span class="material-symbols-rounded">settings</span><span>系统设置</span></a></nav><div class="sidebar-foot"><span class="material-symbols-rounded">deployed_code</span><span><strong>CtYunKeeper</strong><small>版本 v` + esc(s.version) + `</small></span></div></aside><header class="topbar"><button class="icon-button sidebar-toggle" type="button"><span class="material-symbols-rounded">menu</span></button><strong>天翼云电脑自动化管理</strong><div class="topbar-actions"><a class="icon-button" href="/logs" aria-label="查看日志"><span class="material-symbols-rounded">notifications</span></a><form method="post" action="/ctyun/restart"><input type="hidden" name="csrf_token" value="` + esc(token) + `"><button class="icon-button" aria-label="重新加载保活"><span class="material-symbols-rounded">refresh</span></button></form><form method="post" action="/logout"><input type="hidden" name="csrf_token" value="` + esc(token) + `"><button class="icon-button danger-icon" aria-label="退出"><span class="material-symbols-rounded">power_settings_new</span></button></form></div></header><button class="sidebar-backdrop" type="button"></button>`
	}
	fmt.Fprintf(w, "<!doctype html><html lang=zh-CN><head><meta charset=utf-8><meta name=viewport content='width=device-width,initial-scale=1'><meta name=csrf-token content='%s'><title>%s · CtYunKeeper</title><link rel=stylesheet href='/static/app.css?v=%s-ui5'><script src='/static/htmx.min.js' defer></script><script src='/static/app.js?v=%s-ui5' defer></script></head><body data-authenticated='%t'>%s<main class='%s'>%s%s</main></body></html>", esc(token), esc(title), esc(s.version), esc(s.version), auth, nav, mainClass, flash, content)
}
func redirect(w http.ResponseWriter, r *http.Request, path, msg string, isErr bool) {
	key := "notice"
	if isErr {
		key = "error"
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	http.Redirect(w, r, path+sep+key+"="+url.QueryEscape(msg), 303)
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	hash, _ := s.store.Setting("admin_password_hash")
	if hash != "" {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if r.Method == "POST" {
		_ = r.ParseForm()
		if !s.checkCSRF(r) {
			http.Error(w, "CSRF validation failed", 403)
			return
		}
		p := r.FormValue("password")
		if len(p) < 8 || p != r.FormValue("confirmation") {
			redirect(w, r, "/setup", "密码至少 8 位且两次输入必须一致", true)
			return
		}
		_ = s.store.SetSetting("admin_password_hash", security.HashPassword(p))
		s.cookie(w, "ctyun_session", security.SignCookie(s.sessionKey, "authenticated"), true)
		http.Redirect(w, r, "/", 303)
		return
	}
	s.page(w, r, "初始化", `<section class="auth-card"><p class="eyebrow">首次运行</p><h1>设置管理密码</h1><form method=post><input type=hidden name=csrf_token value="{{CSRF}}"><label>密码<input type=password name=password minlength=8 required></label><label>确认密码<input type=password name=confirmation minlength=8 required></label><button class=primary>开始使用</button></form></section>`, false)
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		_ = r.ParseForm()
		if !s.checkCSRF(r) {
			http.Error(w, "CSRF validation failed", 403)
			return
		}
		hash, _ := s.store.Setting("admin_password_hash")
		if !security.VerifyPassword(hash, r.FormValue("password")) {
			redirect(w, r, "/login", "密码不正确", true)
			return
		}
		s.cookie(w, "ctyun_session", security.SignCookie(s.sessionKey, "authenticated"), true)
		http.Redirect(w, r, "/", 303)
		return
	}
	s.page(w, r, "登录", `<section class="auth-card"><p class="eyebrow">管理面板</p><h1>欢迎回来</h1><form method=post><input type=hidden name=csrf_token value="{{CSRF}}"><label>管理密码<input type=password name=password required autofocus></label><button class=primary>登录</button></form></section>`, false)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || !s.checkCSRF(r) {
		http.Error(w, "Forbidden", 403)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "ctyun_session", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", 303)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !s.guard(w, r) {
		return
	}
	content := fmt.Sprintf(`<header class="page-head"><div><p class=eyebrow>运行状态</p><h1>仪表盘</h1><p class=page-subtitle>查看保活服务、账号与自动任务的实时状态</p></div></header><section class=metric-grid id=status-cards hx-get=/partials/status hx-trigger='every 10s'>%s</section><article class="panel info-panel"><div class="panel-head dashboard-info-head"><div class=panel-title><span class="panel-icon dashboard-panel-icon">%s</span><div><p class=eyebrow>运行信息</p><h2>服务状态</h2></div></div><span class=service-health><i></i>服务在线</span></div><dl class=service-facts><div><dt><span class=fact-icon>%s</span><span>程序运行时长</span></dt><dd id=program-uptime data-uptime-seconds="%d">计算中</dd></div><div><dt><span class=fact-icon>%s</span><span>运行架构</span></dt><dd>%s</dd></div><div><dt><span class=fact-icon>%s</span><span>当前版本</span></dt><dd>v%s</dd></div></dl></article>`, s.dashboardMetrics(), dashboardIcon("server"), dashboardIcon("uptime"), int(time.Since(s.manager.Started()).Seconds()), dashboardIcon("cpu"), esc(runtime.GOARCH), dashboardIcon("version"), esc(s.version))
	s.page(w, r, "仪表盘", content, true)
}
func (s *Server) statusPartial(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	fmt.Fprint(w, s.dashboardMetrics())
}

func (s *Server) dashboardMetrics() string {
	accounts, _ := s.store.Accounts()
	runs, _ := s.store.Runs(200)
	state, workers := s.manager.KeepaliveStatus()
	runningAccounts := s.manager.RunningAccountCount()

	keepaliveText := "当前没有云电脑正在保活"
	if workers > 0 {
		keepaliveText = fmt.Sprintf("正在保活 %d 台云电脑", workers)
	}
	var active []storage.Run
	var recent *storage.Run
	for i := range runs {
		if runs[i].Status == "queued" || runs[i].Status == "running" {
			active = append(active, runs[i])
		} else if recent == nil {
			recent = &runs[i]
		}
	}

	activeContent := `<strong class="metric-value small">当前空闲</strong><span class="metric-note">暂无正在执行的自动化任务</span>`
	if len(active) > 0 {
		var list strings.Builder
		fmt.Fprintf(&list, `<strong class="metric-value small">%d 项任务</strong><ul class="metric-task-list">`, len(active))
		for _, run := range active {
			name := run.AccountName
			if name == "" {
				name = fmt.Sprintf("账号 #%d", run.AccountID)
			}
			fmt.Fprintf(&list, `<li><span class="task-state-dot %s"></span><span><b>%s</b><small>%s · %s</small></span></li>`, esc(run.Status), esc(name), esc(dashboardTaskLabel(run.TaskType)), esc(statusLabel(run.Status)))
		}
		list.WriteString(`</ul>`)
		activeContent = list.String()
	}

	recentContent := `<strong class="metric-value small">暂无记录</strong><span class="metric-note">任务结束后将在这里显示结果</span>`
	if recent != nil {
		name := recent.AccountName
		if name == "" {
			name = fmt.Sprintf("账号 #%d", recent.AccountID)
		}
		finishedAt := recent.FinishedAt
		if finishedAt == "" {
			finishedAt = recent.StartedAt
		}
		recentContent = fmt.Sprintf(`<strong class="metric-value small metric-account-name" title="%s">%s</strong><div class=metric-result><span class="pill %s">%s</span><span>%s</span></div><span class=metric-time>%s</span>`, esc(name), esc(name), esc(recent.Status), esc(statusLabel(recent.Status)), esc(dashboardTaskLabel(recent.TaskType)), esc(formatTime(finishedAt)))
	}

	stateTone := "success"
	if strings.Contains(state, "异常") || strings.Contains(state, "失败") || strings.Contains(state, "错误") {
		stateTone = "danger"
	} else if workers == 0 {
		stateTone = "warning"
	}
	return fmt.Sprintf(`<article class="panel metric-card metric-%s"><div class=metric-top><span class=metric-icon>%s</span><span class=metric-label>云电脑保活</span><i class="metric-status-dot %s"></i></div><strong class="metric-value small">%s</strong><span class="metric-note">%s</span></article><article class="panel metric-card metric-accounts"><div class=metric-top><span class=metric-icon>%s</span><span class=metric-label>账号概况</span></div><strong class=metric-number>%d<em>个账号</em></strong><span class=metric-note><b>%d</b> 个正在运行</span></article><article class="panel metric-card metric-card-tasks"><div class=metric-top><span class=metric-icon>%s</span><span class=metric-label>执行中的任务</span></div>%s</article><article class="panel metric-card metric-recent"><div class=metric-top><span class=metric-icon>%s</span><span class=metric-label>最近任务</span></div>%s</article>`, stateTone, dashboardIcon("cloud"), stateTone, esc(state), esc(keepaliveText), dashboardIcon("users"), len(accounts), runningAccounts, dashboardIcon("tasks"), activeContent, dashboardIcon("history"), recentContent)
}

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	var b strings.Builder
	b.WriteString(`<header class=page-head><div><p class=eyebrow>配置</p><h1>账号</h1></div><a class="primary button" href=/accounts/new>添加账号</a></header>`)
	b.WriteString(s.accountTable())
	s.page(w, r, "账号", b.String(), true)
}
func (s *Server) accountTable() string {
	values, _ := s.store.Accounts()
	var b strings.Builder
	b.WriteString(`<article id=account-table class="panel table-panel" hx-get=/partials/accounts hx-trigger="every 5s" hx-swap=outerHTML><div class=table-wrap><table><thead><tr><th>账号</th><th>保活</th><th>AI 对话</th><th>挂机</th><th>操作</th></tr></thead><tbody>`)
	for _, a := range values {
		status := s.manager.AccountStatus(a.ID)
		fmt.Fprintf(&b, `<tr><td><strong>%s</strong><small>%s</small></td><td><span class="pill %s">%s</span><small>%s</small></td><td>%s</td><td>%s</td><td class=actions><a href="/accounts/%d/edit">编辑</a><a href="/accounts/%d/redeem">兑换</a><form method=post action="/accounts/%d/device-verification/start"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=link-button>设备验证</button></form><form method=post action="/accounts/%d/delete"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=danger-link>删除</button></form></td></tr>`, esc(a.Name), esc(mask(a.Username)), map[bool]string{true: "success", false: "warning"}[a.Enabled], map[bool]string{true: "启用", false: "停用"}[a.Enabled], esc(status), esc(a.ChatCron), esc(a.PCCron), a.ID, a.ID, a.ID, a.ID)
	}
	b.WriteString(`</tbody></table></div></article>`)
	return b.String()
}
func (s *Server) accountsPartial(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	content := strings.ReplaceAll(s.accountTable(), "{{CSRF}}", esc(s.csrf(w, r)))
	_, _ = io.WriteString(w, content)
}
func mask(v string) string {
	if len(v) < 6 {
		return v[:min(1, len(v))] + "***"
	}
	return v[:3] + "****" + v[len(v)-3:]
}
func (s *Server) accountNew(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	s.accountForm(w, r, storage.Account{Enabled: true, ChatEnabled: true, PCEnabled: true, ChatCron: "0 3,20 * * *", PCCron: "0 4,6 * * *", DeviceCode: security.RandomToken(24)})
}
func (s *Server) accountForm(w http.ResponseWriter, r *http.Request, a storage.Account) {
	title := "添加账号"
	required := " required"
	if a.ID > 0 {
		title = "编辑账号"
		required = ""
	}
	content := fmt.Sprintf(`<header class=page-head><div><p class=eyebrow>账号配置</p><h1>%s</h1></div><a class="secondary button" href=/accounts>返回</a></header><form method=post action=/accounts/save class="panel form-panel"><input type=hidden name=csrf_token value="{{CSRF}}"><input type=hidden name=account_id value="%d"><fieldset><legend>登录信息</legend><div class=form-grid><label>显示名称<input name=name value="%s" required></label><label>天翼云账号<input name=username value="%s" required></label><label>密码<div class=password-field><input name=password type=password%s placeholder="%s">%s</div></label><label>设备码<input name=device_code value="%s" required></label></div><label class=switch-row><input type=checkbox name=enabled%s><span>启用账号和保活</span></label></fieldset><fieldset><legend>积分任务</legend><div class=schedule-box><label class=switch-row><input type=checkbox name=chat_enabled%s><span>启用 AI 对话积分</span></label><label>Cron 计划<input name=chat_cron value="%s" required></label></div><div class=schedule-box><label class=switch-row><input type=checkbox name=pc_enabled%s><span>启用云电脑挂机</span></label><label>Cron 计划<input name=pc_cron value="%s" required></label></div></fieldset><div class=form-actions><a href=/accounts>取消</a><button class=primary>保存并检查设备</button></div></form>`, title, a.ID, esc(a.Name), esc(a.Username), required, map[bool]string{true: "留空表示不修改", false: "请输入密码"}[a.ID > 0], passwordToggle(), esc(a.DeviceCode), checked(a.Enabled), checked(a.ChatEnabled), esc(a.ChatCron), checked(a.PCEnabled), esc(a.PCCron))
	s.page(w, r, title, content, true)
}
func (s *Server) saveAccount(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) || r.Method != "POST" {
		return
	}
	_ = r.ParseForm()
	if !s.checkCSRF(r) {
		http.Error(w, "CSRF validation failed", 403)
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	a := storage.Account{ID: id, Name: strings.TrimSpace(r.FormValue("name")), Username: strings.TrimSpace(r.FormValue("username")), DeviceCode: strings.TrimSpace(r.FormValue("device_code")), Enabled: r.Form.Has("enabled"), ChatEnabled: r.Form.Has("chat_enabled"), ChatCron: r.FormValue("chat_cron"), PCEnabled: r.Form.Has("pc_enabled"), PCCron: r.FormValue("pc_cron")}
	saved, e := s.store.SaveAccount(a, r.FormValue("password"), s.credentialKey, security.EncryptFernet)
	if e != nil {
		redirect(w, r, "/accounts", e.Error(), true)
		return
	}
	s.store.ClearAuthCache(saved)
	if !a.Enabled {
		s.manager.RestartKeepalive()
		redirect(w, r, "/accounts", "账号已保存（当前停用）", false)
		return
	}
	bound, e := s.manager.BeginVerification(r.Context(), saved)
	if e != nil {
		redirect(w, r, "/accounts", "账号已保存，自动检查失败："+e.Error(), true)
		return
	}
	if bound {
		s.manager.RestartKeepalive()
		redirect(w, r, "/accounts", "账号已保存并开始保活", false)
	} else {
		redirect(w, r, fmt.Sprintf("/accounts/%d/device-verification", saved), "短信验证码已发送", false)
	}
}

func (s *Server) accountRoute(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		return
	}
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	if len(parts) == 3 && parts[2] == "edit" {
		a, e := s.store.Account(id)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		s.accountForm(w, r, a)
		return
	}
	if len(parts) >= 3 && parts[2] == "delete" && r.Method == "POST" {
		if !s.checkCSRF(r) {
			http.Error(w, "Forbidden", 403)
			return
		}
		_ = s.store.DeleteAccount(id)
		s.manager.RestartKeepalive()
		redirect(w, r, "/accounts", "账号已删除", false)
		return
	}
	if len(parts) >= 4 && parts[2] == "device-verification" {
		if parts[3] == "start" && r.Method == "POST" {
			if !s.checkCSRF(r) {
				http.Error(w, "Forbidden", 403)
				return
			}
			bound, e := s.manager.BeginVerification(r.Context(), id)
			if e != nil {
				redirect(w, r, "/accounts", e.Error(), true)
			} else if bound {
				s.manager.RestartKeepalive()
				redirect(w, r, "/accounts", "设备已经绑定", false)
			} else {
				http.Redirect(w, r, fmt.Sprintf("/accounts/%d/device-verification", id), 303)
			}
			return
		}
		if parts[3] == "complete" && r.Method == "POST" {
			if !s.checkCSRF(r) {
				http.Error(w, "Forbidden", 403)
				return
			}
			e := s.manager.CompleteVerification(r.Context(), id, r.FormValue("verification_code"))
			if e != nil {
				redirect(w, r, fmt.Sprintf("/accounts/%d/device-verification", id), e.Error(), true)
			} else {
				redirect(w, r, "/accounts", "设备绑定成功并已开始保活", false)
			}
			return
		}
	}
	if len(parts) == 3 && parts[2] == "device-verification" {
		a, _ := s.store.Account(id)
		content := fmt.Sprintf(`<header class=page-head><div><p class=eyebrow>安全验证</p><h1>绑定设备</h1></div></header><form class="panel form-panel" method=post action="/accounts/%d/device-verification/complete"><input type=hidden name=csrf_token value="{{CSRF}}"><p>验证码已发送到账号 %s，请在 10 分钟内完成。</p><label>短信验证码<input name=verification_code inputmode=numeric minlength=4 maxlength=8 required autofocus></label><button class=primary>完成验证</button></form>`, id, esc(mask(a.Username)))
		s.page(w, r, "设备验证", content, true)
		return
	}
	if len(parts) >= 4 && parts[2] == "tasks" && r.Method == "POST" {
		if !s.checkCSRF(r) {
			http.Error(w, "Forbidden", 403)
			return
		}
		run, e := s.manager.StartTask(id, parts[3], "manual")
		if e != nil {
			redirect(w, r, "/tasks", e.Error(), true)
		} else {
			redirect(w, r, "/tasks", fmt.Sprintf("任务已启动，编号 #%d", run), false)
		}
		return
	}
	if len(parts) >= 3 && parts[2] == "platform-status" && r.Method == "POST" {
		if !s.checkCSRF(r) {
			http.Error(w, "Forbidden", 403)
			return
		}
		v, e := s.manager.RefreshStatus(r.Context(), id)
		if e != nil {
			redirect(w, r, "/tasks", "平台状态查询失败："+e.Error(), true)
		} else {
			redirect(w, r, "/tasks", fmt.Sprintf("状态已更新，总积分 %d", *v.TotalPoints), false)
		}
		return
	}
	if len(parts) >= 3 && parts[2] == "redeem" {
		s.redeem(w, r, id, parts)
		return
	}
	http.NotFound(w, r)
}

func initial(v string) string {
	r := []rune(v)
	if len(r) == 0 {
		return "云"
	}
	return string(r[0])
}
func (s *Server) taskCards(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	accounts, _ := s.store.Accounts()
	platforms, _ := s.store.Platforms()
	now := time.Now()
	var b strings.Builder
	for _, a := range accounts {
		p := platforms[a.ID]
		fresh := platformStatusUpdatedToday(p.UpdatedAt, now)
		points := "--"
		if p.TotalPoints != nil {
			points = strconv.Itoa(*p.TotalPoints)
		}
		pointsLabel := "当前总积分"
		updatedClass := ""
		updatedText := "今日尚未查询"
		if p.UpdatedAt != "" && fresh {
			updatedText = "今日更新于 " + formatTime(p.UpdatedAt)
		} else if p.UpdatedAt != "" {
			pointsLabel = "最近总积分"
			updatedClass = " stale"
			updatedText = "上次查询于 " + formatTime(p.UpdatedAt) + " · 今日待更新"
		}
		accountState := map[bool]string{true: "success", false: "stopped"}[a.Enabled]
		fmt.Fprintf(&b, `<article class="panel launch-card platform-card"><div class=platform-card-head><div class=platform-account><span class=account-avatar>%s</span><span><strong>%s</strong><small><i class="account-state-dot %s"></i>%s</small></span></div><div class=points-summary><small>%s</small><strong>%s</strong></div></div><div class=platform-task-grid>`, esc(initial(a.Name)), esc(a.Name), accountState, map[bool]string{true: "账号启用", false: "账号停用"}[a.Enabled], pointsLabel, points)
		for _, x := range []struct{ k, n string }{{"login", "登录 AI 云电脑"}, {"usage", "使用 1 小时"}, {"chat", "AI 对话"}} {
			t, ok := p.Tasks[x.k]
			if !fresh {
				t = storage.TaskStatus{State: "stale", StateLabel: "今日未查询", Total: map[string]int{"usage": 3600}[x.k]}
				if t.Total == 0 {
					t.Total = 1
				}
			} else if !ok {
				t = storage.TaskStatus{State: "warning", StateLabel: "待查询"}
			}
			progress := 0
			if t.Total > 0 {
				progress = min(100, max(0, t.Current*100/t.Total))
			}
			fmt.Fprintf(&b, `<div class="platform-task %s"><div class=platform-task-top><span class=platform-task-name>%s</span><span class="pill %s">%s</span></div><div class=platform-progress><span style="width:%d%%"></span></div><small>进度 %d/%d</small></div>`, esc(t.State), x.n, esc(t.State), esc(t.StateLabel), progress, t.Current, t.Total)
		}
		fmt.Fprintf(&b, `</div><div class="platform-updated%s"><span class="material-symbols-rounded">schedule</span>%s</div><div class=launch-actions><form method=post action="/accounts/%d/tasks/login"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=secondary%s>%s<span>登陆任务</span></button></form><form method=post action="/accounts/%d/tasks/pc"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=primary%s>%s<span>时长任务</span></button></form><form method=post action="/accounts/%d/tasks/chat"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=secondary%s>%s<span>AI对话任务</span></button></form><form method=post action="/accounts/%d/platform-status/refresh"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=secondary%s>%s<span>任务状态查询</span></button></form></div></article>`, updatedClass, esc(updatedText), a.ID, disabled(!a.Enabled), buttonIcon("login"), a.ID, disabled(!a.Enabled), buttonIcon("usage"), a.ID, disabled(!a.Enabled), buttonIcon("chat"), a.ID, disabled(!a.Enabled), buttonIcon("status"))
	}
	if len(accounts) == 0 {
		b.WriteString(`<article class="panel task-empty-card"><span class="material-symbols-rounded">manage_accounts</span><div class=task-empty-copy><strong>还没有可运行的账号</strong><p>添加账号后，即可在这里查看每日任务状态并快速执行任务。</p></div><a class="secondary button" href=/accounts/new>添加账号</a></article>`)
	}
	fmt.Fprint(w, strings.ReplaceAll(b.String(), "{{CSRF}}", esc(s.csrf(w, r))))
}
func (s *Server) tasks(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	token := s.csrf(w, r)
	r.AddCookie(&http.Cookie{Name: "ctyun_csrf", Value: security.SignCookie(s.sessionKey, token), Path: "/"})
	content := `<header class=page-head><div><p class=eyebrow>自动化</p><h1>任务中心</h1><p class=page-subtitle>按账号查看三项平台任务进度，并独立执行登录、对话与挂机。</p></div></header><section class=task-launch-grid id=task-account-status hx-get=/partials/task-accounts hx-trigger='every 10s' hx-swap=innerHTML>`
	var recorder strings.Builder
	rw := responseWriter{&recorder, http.Header{}}
	s.taskCards(&rw, r)
	content += recorder.String() + `</section>`
	runs, _ := s.store.Runs(100)
	content += `<article class="panel table-panel task-history-panel"><div class=panel-head><div class=panel-title><span class="panel-icon material-symbols-rounded">history</span><div><p class=eyebrow>历史记录</p><h2>任务运行记录</h2></div></div></div><div class=table-wrap><table><thead><tr><th>编号</th><th>任务</th><th>账号</th><th>状态</th><th>开始时间</th><th>操作</th></tr></thead><tbody>`
	for _, v := range runs {
		label := taskLabel(v.TaskType)
		if label == "" {
			label = v.TaskType
		}
		content += fmt.Sprintf(`<tr><td data-label="编号">#%d</td><td data-label="任务">%s</td><td data-label="账号">%s</td><td data-label="状态"><span class="pill %s">%s</span></td><td data-label="开始时间"><time>%s</time></td><td data-label="操作" class=actions><a href="/logs?run_id=%d">查看日志</a>`, v.ID, esc(label), esc(v.AccountName), esc(v.Status), esc(statusLabel(v.Status)), esc(formatTime(v.StartedAt)), v.ID)
		if v.Status == "running" || v.Status == "queued" {
			content += fmt.Sprintf(`<form method=post action="/tasks/%d/stop"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=danger-link>停止</button></form>`, v.ID)
		}
		content += `</td></tr>`
	}
	content += `</tbody></table></div></article>`
	s.page(w, r, "任务", content, true)
}

type responseWriter struct {
	io.Writer
	h http.Header
}

func (r *responseWriter) Header() http.Header { return r.h }
func (r *responseWriter) WriteHeader(int)     {}
func (s *Server) taskRoute(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 3 && parts[2] == "stop" && r.Method == "POST" {
		if !s.checkCSRF(r) {
			http.Error(w, "Forbidden", 403)
			return
		}
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		if s.manager.StopTask(id) {
			redirect(w, r, "/tasks", "任务已停止", false)
		} else {
			redirect(w, r, "/tasks", "任务当前无法停止", true)
		}
		return
	}
	http.NotFound(w, r)
}

func (s *Server) redeem(w http.ResponseWriter, r *http.Request, id int64, parts []string) {
	if r.Method == "POST" {
		if !s.checkCSRF(r) {
			http.Error(w, "Forbidden", 403)
			return
		}
		if len(parts) > 3 && parts[3] == "resolve" {
			_ = s.manager.ResolveRedeem(id, r.FormValue("succeeded") == "1")
			redirect(w, r, fmt.Sprintf("/accounts/%d/redeem", id), "待确认订单状态已处理", false)
			return
		}
		cfg := storage.RedeemConfig{AccountID: id, Enabled: r.Form.Has("enabled"), ProductID: r.FormValue("product_id"), DesktopID: r.FormValue("desktop_id"), MaxTimes: security.Int(r.FormValue("max_times")), ScheduleType: r.FormValue("schedule_type"), IntervalDays: security.Int(r.FormValue("interval_days")), MonthlyDays: r.FormValue("monthly_days")}
		if rewards, _, e := s.manager.RedeemCatalog(r.Context(), id); e == nil {
			for _, p := range rewards {
				if strconv.FormatInt(p.ProductID, 10) == cfg.ProductID {
					cfg.ProductName = p.ProductName
					cfg.ProductType = p.ProductType
					cfg.CostPoints = p.CostPoints
					break
				}
			}
		}
		if e := s.store.SaveRedeem(cfg); e != nil {
			redirect(w, r, r.URL.Path, e.Error(), true)
		} else {
			redirect(w, r, r.URL.Path, "自动兑换配置已保存", false)
		}
		return
	}
	a, _ := s.store.Account(id)
	cfg, _ := s.store.Redeem(id)
	var pending string
	_ = s.store.DB.QueryRow("SELECT last_attempt_status FROM redeem_states WHERE account_id=?", id).Scan(&pending)
	rewards, desktops, e := s.manager.RedeemCatalog(context.Background(), id)
	warning := ""
	if e != nil {
		warning = `<div class="form-error">无法读取实时目录：` + esc(e.Error()) + `</div>`
	}
	var productOptions strings.Builder
	for _, p := range rewards {
		sel := ""
		if strconv.FormatInt(p.ProductID, 10) == cfg.ProductID {
			sel = " selected"
		}
		fmt.Fprintf(&productOptions, `<option value="%d" data-name="%s" data-type="%s" data-cost="%d"%s>%s（%d 积分）</option>`, p.ProductID, esc(p.ProductName), esc(p.ProductType), p.CostPoints, sel, esc(p.ProductName), p.CostPoints)
	}
	var desktopOptions strings.Builder
	for _, d := range desktops {
		sel := ""
		if d.ID() == cfg.DesktopID {
			sel = " selected"
		}
		fmt.Fprintf(&desktopOptions, `<option value="%s"%s>%s</option>`, esc(d.ID()), sel, esc(d.Name()))
	}
	pendingPanel := ""
	if pending == "pending" {
		pendingPanel = fmt.Sprintf(`<article class="panel form-panel"><h2>上一笔订单待确认</h2><p>为避免重复扣除积分，自动兑换已暂停。请在平台核对订单后选择结果。</p><div class=form-actions><form method=post action="/accounts/%d/redeem/resolve"><input type=hidden name=csrf_token value="{{CSRF}}"><input type=hidden name=succeeded value=1><button class=primary>确认已成功</button></form><form method=post action="/accounts/%d/redeem/resolve"><input type=hidden name=csrf_token value="{{CSRF}}"><input type=hidden name=succeeded value=0><button class=secondary>确认未成功</button></form></div></article>`, id, id)
	}
	content := fmt.Sprintf(`<header class=page-head><div><p class=eyebrow>积分奖励</p><h1>%s · 自动兑换</h1></div><div class=form-actions><form method=post action="/accounts/%d/tasks/redeem"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=secondary>立即检查兑换</button></form><a class="secondary button" href=/accounts>返回</a></div></header>%s%s<form class="panel form-panel" method=post><input type=hidden name=csrf_token value="{{CSRF}}"><label class=switch-row><input type=checkbox name=enabled%s><span>启用自动兑换（默认关闭）</span></label><div class=form-grid><label>奖励商品<select name=product_id required>%s</select></label><label>目标云电脑<select name=desktop_id required>%s</select></label><label>单次最多兑换次数<input type=number name=max_times min=1 value="%d"></label><label>计划<select name=schedule_type><option value=daily%s>每天检查</option><option value=interval%s>按间隔天数</option><option value=monthly%s>指定每月日期</option></select></label><label>间隔天数<input type=number name=interval_days min=1 value="%d"></label><label>每月日期<input name=monthly_days value="%s" placeholder="1,15,28"></label></div><p class=muted>提交订单前会重新校验商品、积分和云电脑；不确定结果会进入待人工确认状态，防止重复兑换。</p><button class=primary>保存配置</button></form>`, esc(a.Name), id, warning, pendingPanel, checked(cfg.Enabled), productOptions.String(), desktopOptions.String(), cfg.MaxTimes, selected(cfg.ScheduleType == "daily"), selected(cfg.ScheduleType == "interval"), selected(cfg.ScheduleType == "monthly"), cfg.IntervalDays, esc(cfg.MonthlyDays))
	s.page(w, r, "自动兑换", content, true)
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	runs, _ := s.store.Runs(200)
	path := filepath.Join(s.dataDir, "logs", "ctyun.log")
	selectedID := r.URL.Query().Get("run_id")
	var selected storage.Run
	if selectedID != "" {
		for _, run := range runs {
			if strconv.FormatInt(run.ID, 10) == selectedID {
				selected = run
				path = run.LogPath
				break
			}
		}
	}
	raw, _ := os.ReadFile(path)
	fileSize := int64(len(raw))
	if len(raw) > 256<<10 {
		raw = raw[len(raw)-(256<<10):]
	}
	if len(raw) == 0 {
		raw = []byte("暂无日志输出。")
	} else {
		raw = []byte(reverseLogText(string(raw)))
	}
	logTitle := "运行日志"
	logMeta := "CtYunKeeper 服务与保活状态"
	indicator := `<span class="live-indicator"><i></i>实时输出</span>`
	attrs := ` id="log-output" data-stream="/logs/system/stream" data-offset="` + strconv.FormatInt(fileSize, 10) + `"`
	if selected.ID != 0 {
		label := taskLabel(selected.TaskType)
		if label == "" {
			label = selected.TaskType
		}
		logTitle = fmt.Sprintf("#%d · %s", selected.ID, label)
		logMeta = fmt.Sprintf("%s · %s · %s", selected.AccountName, statusLabel(selected.Status), formatTime(selected.StartedAt))
		attrs = ` id="log-output" data-stream="/logs/` + strconv.FormatInt(selected.ID, 10) + `/stream" data-offset="` + strconv.FormatInt(fileSize, 10) + `"`
		indicator = `<span class="live-indicator"><i></i>实时输出</span>`
	}
	clearID := "0"
	if selected.ID != 0 {
		clearID = strconv.FormatInt(selected.ID, 10)
	}
	clearAction := `<div class=log-panel-actions><form method=post action=/logs/clear data-confirm="确认清空当前日志？清空后无法恢复。"><input type=hidden name=csrf_token value="{{CSRF}}"><input type=hidden name=run_id value="` + clearID + `"><button class="icon-button log-clear-button" type=submit title="清空当前日志" aria-label="清空当前日志"><img src=/static/delete-sweep.svg alt=""></button></form>` + indicator + `</div>`
	content := `<header class=page-head><div><p class=eyebrow>诊断</p><h1>日志中心</h1><p class=page-subtitle>集中查看系统运行日志和每次自动化任务的独立日志。</p></div></header><section class=log-layout>` + s.logSources(runs, selected.ID) + `<article class="panel log-panel"><div class=log-panel-head><div><h2>` + esc(logTitle) + `</h2><p>` + esc(logMeta) + `</p></div>` + clearAction + `</div><pre class=log-output` + attrs + `>` + esc(string(raw)) + `</pre></article></section>`
	s.page(w, r, "日志", content, true)
}
func (s *Server) clearLog(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) || r.Method != http.MethodPost {
		return
	}
	if !s.checkCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	runID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("run_id")), 10, 64)
	if err != nil || runID < 0 {
		redirect(w, r, "/logs", "日志类型无效", true)
		return
	}
	if err := s.manager.ClearLog(runID); err != nil {
		redirect(w, r, "/logs", "清空日志失败："+err.Error(), true)
		return
	}
	target := "/logs"
	if runID > 0 {
		target += "?run_id=" + strconv.FormatInt(runID, 10)
	}
	redirect(w, r, target, "当前日志已清空", false)
}
func (s *Server) logSources(runs []storage.Run, selectedID int64) string {
	var sources strings.Builder
	partialURL := "/partials/log-sources"
	if selectedID != 0 {
		partialURL += "?run_id=" + strconv.FormatInt(selectedID, 10)
	}
	systemActive := "active"
	if selectedID != 0 {
		systemActive = ""
	}
	fmt.Fprintf(&sources, `<aside id=log-sources class="panel log-sources" hx-get="%s" hx-trigger="every 5s" hx-swap=outerHTML><h2>系统日志</h2><a class="%s" href="/logs"><strong>运行日志</strong><small>CtYunKeeper 服务输出</small></a><h2>任务日志</h2>`, partialURL, systemActive)
	if len(runs) == 0 {
		sources.WriteString(`<p class=log-empty>还没有任务记录</p>`)
	}
	for _, run := range runs {
		active := ""
		if run.ID == selectedID {
			active = "active"
		}
		label := taskLabel(run.TaskType)
		if label == "" {
			label = run.TaskType
		}
		fmt.Fprintf(&sources, `<a class="%s" href="/logs?run_id=%d"><span class=log-source-title><strong>#%d · %s</strong><span class="status-dot %s"></span></span><small>%s · %s</small></a>`, active, run.ID, run.ID, esc(label), esc(run.Status), esc(run.AccountName), esc(formatTime(run.StartedAt)))
	}
	sources.WriteString(`</aside>`)
	return sources.String()
}
func (s *Server) logSourcesPartial(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	selectedID, _ := strconv.ParseInt(r.URL.Query().Get("run_id"), 10, 64)
	runs, _ := s.store.Runs(200)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, s.logSources(runs, selectedID))
}
func (s *Server) logStream(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[2] != "stream" {
		http.NotFound(w, r)
		return
	}
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	run := storage.Run{}
	path := filepath.Join(s.dataDir, "logs", "ctyun.log")
	systemLog := parts[1] == "system"
	if !systemLog {
		runs, _ := s.store.Runs(500)
		for _, v := range runs {
			if v.ID == id {
				run = v
				path = v.LogPath
				break
			}
		}
		if run.ID == 0 {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if offset < 0 {
		offset = 0
	}
	for {
		f, e := os.Open(path)
		if e == nil {
			if info, statErr := f.Stat(); statErr == nil && info.Size() < offset {
				offset = 0
				fmt.Fprint(w, "event: reset\ndata: true\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
			_, _ = f.Seek(offset, io.SeekStart)
			raw, _ := io.ReadAll(f)
			offset, _ = f.Seek(0, io.SeekCurrent)
			f.Close()
			if len(raw) > 0 {
				fmt.Fprintf(w, "data: %q\n\n", reverseLogText(string(raw)))
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
		if !systemLog {
			runs, _ := s.store.Runs(500)
			active := false
			for _, v := range runs {
				if v.ID == id && (v.Status == "running" || v.Status == "queued") {
					active = true
					break
				}
			}
			if !active {
				fmt.Fprint(w, "event: done\ndata: true\n\n")
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(time.Second):
		}
	}
}
func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) || r.Method != "POST" {
		return
	}
	if !s.checkCSRF(r) {
		http.Error(w, "Forbidden", 403)
		return
	}
	s.manager.RestartKeepalive()
	redirect(w, r, "/", "保活服务已重新加载", false)
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	content := fmt.Sprintf(`<header class=page-head><div><p class=eyebrow>系统</p><h1>系统设置</h1><p class=page-subtitle>管理访问安全、运行环境与日志存储策略。</p></div></header><section class=settings-grid><form method=post action=/settings/password class="panel form-panel settings-card password-settings-card"><input type=hidden name=csrf_token value="{{CSRF}}"><div class=settings-card-head><span class="panel-icon material-symbols-rounded">account_circle</span><div><h2>访问安全</h2><small>更新管理面板的访问密码</small></div></div><div class=settings-fields><label>当前密码<div class=password-field><input name=current_password type=password required>%s</div></label><label>新密码<div class=password-field><input name=password type=password minlength=8 required>%s</div></label><label>确认新密码<div class=password-field><input name=confirmation type=password minlength=8 required>%s</div></label></div><p class=settings-hint>新密码至少需要 8 位字符</p><button class="primary settings-primary-action">更新密码</button></form><article class="panel info-panel settings-card deployment-settings-card"><div class=settings-card-head><span class="panel-icon material-symbols-rounded">deployed_code</span><div><h2>部署信息</h2><small>CtYunKeeper 当前运行环境</small></div></div><dl class=deployment-facts><div><dt>服务端口</dt><dd>9845</dd></div><div><dt>当前版本</dt><dd>v%s</dd></div><div><dt>运行方式</dt><dd>Go · Alpine · 单进程</dd></div></dl></article><article class="panel form-panel settings-card log-settings-panel"><div class="settings-card-head log-settings-head"><span class="panel-icon neutral material-symbols-rounded">history</span><div><h2>日志管理</h2><small>设置应用日志的保留与清理方式</small></div><form method=post action=/settings/logs/clear data-confirm="确认清空全部日志？该操作无法恢复。"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=danger-button type=submit><img src=/static/delete-forever.svg alt=""><span>清空全部日志</span></button></form></div><form class=retention-form method=post action=/settings/logs><input type=hidden name=csrf_token value="{{CSRF}}"><label>日志保留天数<div class=number-field><input name=retention_days type=number min=1 max=3650 value="%d" required><span>天</span></div></label><button class=primary>保存日志设置</button><p class=retention-note>每天自动清理超过保留期限的系统日志和已结束任务日志，正在执行的任务不会被删除。</p></form></article></section>`, passwordToggle(), passwordToggle(), passwordToggle(), esc(s.version), s.manager.LogRetentionDays())
	s.page(w, r, "设置", content, true)
}
func (s *Server) logSettings(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) || r.Method != http.MethodPost {
		return
	}
	if !s.checkCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	days, err := strconv.Atoi(strings.TrimSpace(r.FormValue("retention_days")))
	if err != nil || days < 1 || days > 3650 {
		redirect(w, r, "/settings", "日志保留天数必须在 1 到 3650 天之间", true)
		return
	}
	if err := s.manager.SetLogRetentionDays(days); err != nil {
		redirect(w, r, "/settings", "保存日志设置失败："+err.Error(), true)
		return
	}
	redirect(w, r, "/settings", "日志设置已保存", false)
}
func (s *Server) clearAllLogs(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) || r.Method != http.MethodPost {
		return
	}
	if !s.checkCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err := s.manager.ClearAllLogs(); err != nil {
		redirect(w, r, "/settings", "清空全部日志失败："+err.Error(), true)
		return
	}
	redirect(w, r, "/settings", "全部日志已清空", false)
}
func (s *Server) password(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) || r.Method != "POST" {
		return
	}
	if !s.checkCSRF(r) {
		http.Error(w, "Forbidden", 403)
		return
	}
	old, _ := s.store.Setting("admin_password_hash")
	p := r.FormValue("password")
	if !security.VerifyPassword(old, r.FormValue("current_password")) {
		redirect(w, r, "/settings", "当前密码不正确", true)
		return
	}
	if len(p) < 8 || p != r.FormValue("confirmation") {
		redirect(w, r, "/settings", "新密码至少 8 位且两次输入一致", true)
		return
	}
	_ = s.store.SetSetting("admin_password_hash", security.HashPassword(p))
	redirect(w, r, "/settings", "管理密码已更新", false)
}
func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(s.manager.MarshalStatus())
}
func (s *Server) apiAccountRoute(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	if r.Method != "POST" || !s.checkCSRF(r) {
		http.Error(w, `{"error":"forbidden"}`, 403)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 5 || parts[0] != "api" || parts[1] != "accounts" || parts[3] != "tasks" {
		http.NotFound(w, r)
		return
	}
	id, _ := strconv.ParseInt(parts[2], 10, 64)
	run, e := s.manager.StartTask(id, parts[4], "webmcp")
	w.Header().Set("Content-Type", "application/json")
	if e != nil {
		w.WriteHeader(409)
		fmt.Fprintf(w, `{"error":%q}`, e.Error())
		return
	}
	fmt.Fprintf(w, `{"run_id":%d,"status":"queued"}`, run)
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = []ctyun.Reward{}
