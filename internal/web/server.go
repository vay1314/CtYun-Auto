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
	"strconv"
	"strings"
	"time"

	"github.com/yin26287903/ctyun-auto/internal/ctyun"
	"github.com/yin26287903/ctyun-auto/internal/security"
	"github.com/yin26287903/ctyun-auto/internal/service"
	"github.com/yin26287903/ctyun-auto/internal/storage"
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
	s.mux.HandleFunc("/partials/task-accounts", s.taskCards)
	s.mux.HandleFunc("/tasks/", s.taskRoute)
	s.mux.HandleFunc("/tasks", s.tasks)
	s.mux.HandleFunc("/logs", s.logs)
	s.mux.HandleFunc("/logs/", s.logStream)
	s.mux.HandleFunc("/ctyun/restart", s.restart)
	s.mux.HandleFunc("/settings/password", s.password)
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
		nav = `<aside class="sidebar" id="sidebar"><a class="brand" href="/"><span class="brand-mark material-symbols-rounded">cloud_sync</span><span><strong>CtYun Auto</strong><small>云电脑管理台</small></span></a><nav><span class="nav-section">管理</span><a href="/"><span class="material-symbols-rounded">dashboard</span><span>仪表盘</span></a><a href="/accounts"><span class="material-symbols-rounded">manage_accounts</span><span>账号管理</span></a><a href="/tasks"><span class="material-symbols-rounded">schedule</span><span>任务中心</span></a><span class="nav-section">系统</span><a href="/logs"><span class="material-symbols-rounded">terminal</span><span>运行日志</span></a><a href="/settings"><span class="material-symbols-rounded">settings</span><span>系统设置</span></a></nav><div class="sidebar-foot"><span class="material-symbols-rounded">deployed_code</span><span><strong>ctyun-auto</strong><small>版本 v` + esc(s.version) + `</small></span></div></aside><header class="topbar"><button class="icon-button sidebar-toggle" type="button"><span class="material-symbols-rounded">menu</span></button><strong>天翼云电脑自动化管理</strong><div class="topbar-actions"><a class="icon-button" href="/logs"><span class="material-symbols-rounded">notifications</span></a><form method="post" action="/ctyun/restart"><input type="hidden" name="csrf_token" value="` + esc(token) + `"><button class="icon-button"><span class="material-symbols-rounded">refresh</span></button></form><form method="post" action="/logout"><input type="hidden" name="csrf_token" value="` + esc(token) + `"><button class="icon-button danger-icon"><span class="material-symbols-rounded">power_settings_new</span></button></form></div></header><button class="sidebar-backdrop" type="button"></button>`
	}
	fmt.Fprintf(w, "<!doctype html><html lang=zh-CN><head><meta charset=utf-8><meta name=viewport content='width=device-width,initial-scale=1'><meta name=csrf-token content='%s'><title>%s · ctyun-auto</title><link rel=stylesheet href='/static/app.css?v=%s'><script src='/static/htmx.min.js' defer></script><script src='/static/app.js?v=%s' defer></script></head><body data-authenticated='%t'>%s<main class='%s'>%s%s</main></body></html>", esc(token), esc(title), esc(s.version), esc(s.version), auth, nav, mainClass, flash, content)
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
	accounts, _ := s.store.Accounts()
	runs, _ := s.store.Runs(8)
	state, workers := s.manager.KeepaliveStatus()
	ready := 0
	for _, a := range accounts {
		if a.Enabled && a.DeviceStatus != "pending" {
			ready++
		}
	}
	recent := "暂无任务记录"
	if len(runs) > 0 {
		recent = esc(runs[0].TaskType + " · " + runs[0].Status)
	}
	content := fmt.Sprintf(`<header class="page-head"><div><p class=eyebrow>运行状态</p><h1>仪表盘</h1><p>查看保活服务、账号与自动任务的实时状态</p></div></header><section class=metric-grid id=status-cards hx-get=/partials/status hx-trigger='every 10s'><article class="panel metric-card"><small>CtYun 保活</small><strong class=success-text>%s</strong><span>已建立 %d 个云电脑连接</span></article><article class="panel metric-card"><small>账号</small><strong>%d<small>/%d</small></strong><span>可运行 / 全部</span></article><article class="panel metric-card"><small>执行中的任务</small><strong>%d</strong><span>后台任务</span></article><article class="panel metric-card"><small>最近任务</small><strong class=small-value>%s</strong></article></section><article class="panel info-panel"><p class=eyebrow>运行信息</p><h2>服务状态</h2><dl><div><dt>程序运行</dt><dd id=program-uptime data-uptime-seconds="%d">计算中</dd></div><div><dt>运行架构</dt><dd>Go 单进程</dd></div><div><dt>版本</dt><dd>v%s</dd></div></dl></article>`, esc(state), workers, ready, len(accounts), s.manager.ActiveCount(), recent, int(time.Since(s.manager.Started()).Seconds()), esc(s.version))
	s.page(w, r, "仪表盘", content, true)
}
func (s *Server) statusPartial(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	accounts, _ := s.store.Accounts()
	runs, _ := s.store.Runs(1)
	recent := "暂无"
	if len(runs) > 0 {
		recent = runs[0].TaskType + " · " + runs[0].Status
	}
	state, workers := s.manager.KeepaliveStatus()
	fmt.Fprintf(w, `<article class="panel metric-card"><small>CtYun 保活</small><strong class=success-text>%s</strong><span>已建立 %d 个云电脑连接</span></article><article class="panel metric-card"><small>账号</small><strong>%d</strong><span>已配置账号</span></article><article class="panel metric-card"><small>执行中的任务</small><strong>%d</strong><span>后台任务</span></article><article class="panel metric-card"><small>最近任务</small><strong class=small-value>%s</strong></article>`, esc(state), workers, len(accounts), s.manager.ActiveCount(), esc(recent))
}

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	values, _ := s.store.Accounts()
	var b strings.Builder
	b.WriteString(`<header class=page-head><div><p class=eyebrow>配置</p><h1>账号</h1></div><a class="primary button" href=/accounts/new>添加账号</a></header><article class="panel table-panel"><div class=table-wrap><table><thead><tr><th>账号</th><th>保活</th><th>AI 对话</th><th>挂机</th><th>操作</th></tr></thead><tbody>`)
	for _, a := range values {
		status := s.manager.AccountStatus(a.ID)
		fmt.Fprintf(&b, `<tr><td><strong>%s</strong><small>%s</small></td><td><span class="pill %s">%s</span><small>%s</small></td><td>%s</td><td>%s</td><td class=actions><a href="/accounts/%d/edit">编辑</a><a href="/accounts/%d/redeem">兑换</a><form method=post action="/accounts/%d/device-verification/start"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=link-button>设备验证</button></form><form method=post action="/accounts/%d/delete"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=danger-link>删除</button></form></td></tr>`, esc(a.Name), esc(mask(a.Username)), map[bool]string{true: "success", false: "warning"}[a.Enabled], map[bool]string{true: "启用", false: "停用"}[a.Enabled], esc(status), esc(a.ChatCron), esc(a.PCCron), a.ID, a.ID, a.ID, a.ID)
	}
	b.WriteString(`</tbody></table></div></article>`)
	s.page(w, r, "账号", b.String(), true)
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
	content := fmt.Sprintf(`<header class=page-head><div><p class=eyebrow>账号配置</p><h1>%s</h1></div><a class="secondary button" href=/accounts>返回</a></header><form method=post action=/accounts/save class="panel form-panel"><input type=hidden name=csrf_token value="{{CSRF}}"><input type=hidden name=account_id value="%d"><fieldset><legend>登录信息</legend><div class=form-grid><label>显示名称<input name=name value="%s" required></label><label>天翼云账号<input name=username value="%s" required></label><label>密码<input name=password type=password%s placeholder="%s"></label><label>设备码<input name=device_code value="%s" required></label></div><label class=switch-row><input type=checkbox name=enabled%s><span>启用账号和保活</span></label></fieldset><fieldset><legend>积分任务</legend><div class=schedule-box><label class=switch-row><input type=checkbox name=chat_enabled%s><span>启用 AI 对话积分</span></label><label>Cron 计划<input name=chat_cron value="%s" required></label></div><div class=schedule-box><label class=switch-row><input type=checkbox name=pc_enabled%s><span>启用云电脑挂机</span></label><label>Cron 计划<input name=pc_cron value="%s" required></label></div></fieldset><div class=form-actions><a href=/accounts>取消</a><button class=primary>保存并检查设备</button></div></form>`, title, a.ID, esc(a.Name), esc(a.Username), required, map[bool]string{true: "留空表示不修改", false: "请输入密码"}[a.ID > 0], esc(a.DeviceCode), checked(a.Enabled), checked(a.ChatEnabled), esc(a.ChatCron), checked(a.PCEnabled), esc(a.PCCron))
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
	var b strings.Builder
	for _, a := range accounts {
		p := platforms[a.ID]
		points := "--"
		if p.TotalPoints != nil {
			points = strconv.Itoa(*p.TotalPoints)
		}
		fmt.Fprintf(&b, `<article class="panel launch-card platform-card"><div class=platform-card-head><div class=platform-account><span class=account-avatar>%s</span><span><strong>%s</strong><small>%s</small></span></div><div class=points-summary><small>当前总积分</small><strong>%s</strong></div></div><div class=platform-task-grid>`, esc(initial(a.Name)), esc(a.Name), map[bool]string{true: "账号启用", false: "账号停用"}[a.Enabled], points)
		for _, x := range []struct{ k, n string }{{"login", "登录 AI 云电脑"}, {"usage", "使用 1 小时"}, {"chat", "AI 对话"}} {
			t, ok := p.Tasks[x.k]
			if !ok {
				t = storage.TaskStatus{State: "warning", StateLabel: "待查询"}
			}
			fmt.Fprintf(&b, `<div class=platform-task><span class=platform-task-name>%s</span><span class="pill %s">%s</span><small>进度 %d/%d</small></div>`, x.n, esc(t.State), esc(t.StateLabel), t.Current, t.Total)
		}
		fmt.Fprintf(&b, `</div><div class=platform-updated>%s</div><div class=launch-actions><form method=post action="/accounts/%d/tasks/chat"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=secondary%s>运行 AI 对话</button></form><form method=post action="/accounts/%d/tasks/pc"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=primary%s>运行挂机</button></form><form method=post action="/accounts/%d/platform-status/refresh"><input type=hidden name=csrf_token value="{{CSRF}}"><button class=secondary%s>查询任务状态</button></form></div></article>`, esc(p.UpdatedAt), a.ID, disabled(!a.Enabled), a.ID, disabled(!a.Enabled), a.ID, disabled(!a.Enabled))
	}
	fmt.Fprint(w, strings.ReplaceAll(b.String(), "{{CSRF}}", esc(s.csrf(w, r))))
}
func (s *Server) tasks(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	token := s.csrf(w, r)
	r.AddCookie(&http.Cookie{Name: "ctyun_csrf", Value: security.SignCookie(s.sessionKey, token), Path: "/"})
	content := `<header class=page-head><div><p class=eyebrow>自动化</p><h1>任务</h1></div></header><section class=task-launch-grid id=task-account-status hx-get=/partials/task-accounts hx-trigger='every 10s' hx-swap=innerHTML>`
	var recorder strings.Builder
	rw := responseWriter{&recorder, http.Header{}}
	s.taskCards(&rw, r)
	content += recorder.String() + `</section>`
	runs, _ := s.store.Runs(100)
	content += `<article class="panel table-panel"><div class=panel-head><div><p class=eyebrow>历史</p><h2>运行记录</h2></div></div><div class=table-wrap><table><thead><tr><th>编号</th><th>任务</th><th>账号</th><th>状态</th><th>开始时间</th><th>操作</th></tr></thead><tbody>`
	for _, v := range runs {
		content += fmt.Sprintf(`<tr><td>#%d</td><td>%s</td><td>%s</td><td><span class="pill %s">%s</span></td><td>%s</td><td class=actions><a href="/logs?run_id=%d">日志</a>`, v.ID, esc(v.TaskType), esc(v.AccountName), esc(v.Status), esc(v.Status), esc(v.StartedAt), v.ID)
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
	path := filepath.Join(s.dataDir, "logs", "ctyun.log")
	if id := r.URL.Query().Get("run_id"); id != "" {
		runs, _ := s.store.Runs(200)
		for _, v := range runs {
			if strconv.FormatInt(v.ID, 10) == id {
				path = v.LogPath
				break
			}
		}
	}
	raw, _ := os.ReadFile(path)
	if len(raw) > 256<<10 {
		raw = raw[len(raw)-(256<<10):]
	}
	attrs := ""
	if id := r.URL.Query().Get("run_id"); id != "" {
		attrs = ` id="log-output" data-stream="/logs/` + esc(id) + `/stream"`
	}
	content := `<header class=page-head><div><p class=eyebrow>诊断</p><h1>运行日志</h1></div></header><article class="panel log-panel"><pre class=log-output` + attrs + `>` + esc(string(raw)) + `</pre></article>`
	s.page(w, r, "日志", content, true)
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
	runs, _ := s.store.Runs(500)
	var run storage.Run
	for _, v := range runs {
		if v.ID == id {
			run = v
			break
		}
	}
	if run.ID == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	var offset int64
	for {
		f, e := os.Open(run.LogPath)
		if e == nil {
			_, _ = f.Seek(offset, io.SeekStart)
			raw, _ := io.ReadAll(f)
			offset, _ = f.Seek(0, io.SeekCurrent)
			f.Close()
			if len(raw) > 0 {
				fmt.Fprintf(w, "data: %q\n\n", string(raw))
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
		runs, _ = s.store.Runs(500)
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
	content := fmt.Sprintf(`<header class=page-head><div><p class=eyebrow>系统</p><h1>设置</h1></div></header><section class=settings-grid><form method=post action=/settings/password class="panel form-panel"><input type=hidden name=csrf_token value="{{CSRF}}"><h2>修改管理密码</h2><label>当前密码<input name=current_password type=password required></label><label>新密码<input name=password type=password minlength=8 required></label><label>确认新密码<input name=confirmation type=password minlength=8 required></label><button class=primary>更新密码</button></form><article class="panel info-panel"><h2>当前部署</h2><dl><div><dt>端口</dt><dd>9845</dd></div><div><dt>版本</dt><dd>v%s</dd></div><div><dt>运行方式</dt><dd>Go / Alpine / 单进程</dd></div></dl></article></section>`, esc(s.version))
	s.page(w, r, "设置", content, true)
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
