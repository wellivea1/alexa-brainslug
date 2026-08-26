package web

import (
	"crypto/subtle"
	_ "embed"
	"errors"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/adminauth"
)

const (
	adminSessionCookie = "brainslug_admin_session"
	setupMode          = "setup"
	setupTitle         = "Create owner account"
	setupSubmitLabel   = "Create account"
	setupDescription   = "Protect the local management portal before configuring providers and history."
	loginMode          = "login"
	loginTitle         = "Sign in"
	loginDescription   = "Use the owner account created during first-run setup."
)

//go:embed templates/auth.html
var authTemplateSource string

var authTemplate = template.Must(template.New("management-auth").Parse(authTemplateSource))

// AdminPortal serves first-run ownership, login, logout, and the authorization
// middleware around every private management route.
type AdminPortal struct {
	auth            *adminauth.Manager
	trustSetupProxy bool
}

// AdminPortalOptions configures an explicitly trusted local setup proxy.
type AdminPortalOptions struct {
	TrustSetupProxy bool
}

// NewAdminPortal builds the management authentication boundary.
func NewAdminPortal(auth *adminauth.Manager, configured ...AdminPortalOptions) *AdminPortal {
	options := AdminPortalOptions{}
	if len(configured) > 0 {
		options = configured[0]
	}
	return &AdminPortal{auth: auth, trustSetupProxy: options.TrustSetupProxy}
}

// HandleSetupGet renders first-run setup only through a loopback hostname.
func (p *AdminPortal) HandleSetupGet(w http.ResponseWriter, r *http.Request) {
	if !p.setupRequestAllowed(r) {
		http.Error(w, "initial setup is available only through a loopback address", http.StatusForbidden)
		return
	}
	if p.auth.Configured() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	p.render(w, http.StatusOK, authPageData{
		Mode: setupMode, Title: setupTitle, SubmitLabel: setupSubmitLabel,
		Description: setupDescription,
	})
}

// HandleSetupPost creates the one local owner and logs it in.
func (p *AdminPortal) HandleSetupPost(w http.ResponseWriter, r *http.Request) {
	if !p.setupRequestAllowed(r) {
		http.Error(w, "initial setup is available only through a loopback address", http.StatusForbidden)
		return
	}
	if p.auth.Configured() {
		http.Error(w, "management account is already configured", http.StatusConflict)
		return
	}
	if !p.authorizePublicForm(w, r) {
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")
	if password != r.PostFormValue("password_confirmation") {
		p.render(w, http.StatusBadRequest, authPageData{
			Mode: setupMode, Title: setupTitle, SubmitLabel: setupSubmitLabel, Username: username,
			Description: setupDescription,
			FormError:   "Passwords do not match.",
		})
		return
	}
	session, err := p.auth.CreateOwner(username, password)
	if err != nil {
		p.render(w, http.StatusBadRequest, authPageData{
			Mode: setupMode, Title: setupTitle, SubmitLabel: setupSubmitLabel, Username: username,
			Description: setupDescription,
			FormError:   err.Error(),
		})
		return
	}
	p.setSessionCookie(w, r, session)
	dashboardRedirect(w, r, "/?setup=complete")
}

// HandleLoginGet renders login or sends an unconfigured install to setup.
func (p *AdminPortal) HandleLoginGet(w http.ResponseWriter, r *http.Request) {
	if !p.auth.Configured() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if _, ok := p.authenticatedUsername(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	p.render(w, http.StatusOK, authPageData{
		Mode: loginMode, Title: loginTitle, SubmitLabel: loginTitle,
		Description: loginDescription,
	})
}

// HandleLoginPost verifies credentials with per-account and per-source limits.
func (p *AdminPortal) HandleLoginPost(w http.ResponseWriter, r *http.Request) {
	if !p.auth.Configured() {
		http.Error(w, "management account is not configured", http.StatusConflict)
		return
	}
	if !p.authorizePublicForm(w, r) {
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	session, err := p.auth.Login(username, r.PostFormValue("password"), r.RemoteAddr)
	if err != nil {
		status := http.StatusUnauthorized
		message := "Invalid username or password."
		if errors.Is(err, adminauth.ErrRateLimited) {
			status = http.StatusTooManyRequests
			message = "Too many login attempts. Try again later."
		}
		p.render(w, status, authPageData{
			Mode: loginMode, Title: loginTitle, SubmitLabel: loginTitle, Username: username,
			Description: loginDescription, FormError: message,
		})
		return
	}
	p.setSessionCookie(w, r, session)
	dashboardRedirect(w, r, "/")
}

// HandleLogout invalidates the current session after the normal CSRF and
// origin checks used by other management mutations.
func (p *AdminPortal) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if !p.authorizePublicForm(w, r) {
		return
	}
	if cookie, err := r.Cookie(adminSessionCookie); err == nil {
		p.auth.Logout(cookie.Value)
	}
	// #nosec G124 -- HttpOnly and Strict SameSite are fixed; Secure is enabled for HTTPS and disabled only for loopback HTTP.
	http.SetCookie(w, &http.Cookie{
		Name: adminSessionCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: requestIsHTTPS(r), SameSite: http.SameSiteStrictMode,
		MaxAge: -1, Expires: time.Unix(1, 0).UTC(),
	})
	dashboardRedirect(w, r, "/login")
}

// Require enforces authentication server-side for every private route.
func (p *AdminPortal) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, ok := p.authenticatedUsername(r)
		if !ok {
			if r.Method == http.MethodGet && (r.URL.Path == "/" || strings.Contains(r.Header.Get("Accept"), "text/html")) {
				target := "/login"
				if !p.auth.Configured() {
					target = "/setup"
				}
				http.Redirect(w, r, target, http.StatusSeeOther)
				return
			}
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(adminauth.ContextWithUsername(r.Context(), username)))
	})
}

func (p *AdminPortal) authenticatedUsername(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(adminSessionCookie)
	if err != nil {
		return "", false
	}
	return p.auth.ValidateSession(cookie.Value)
}

func (p *AdminPortal) authorizePublicForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "management form could not be read", http.StatusBadRequest)
		return false
	}
	if subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf_token")), []byte(p.auth.CSRFToken())) != 1 {
		http.Error(w, "invalid management form token", http.StatusForbidden)
		return false
	}
	if err := sameOrigin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return false
	}
	return true
}

func (p *AdminPortal) setSessionCookie(w http.ResponseWriter, r *http.Request, session adminauth.Session) {
	// #nosec G124 -- HttpOnly and Strict SameSite are fixed; Secure is enabled for HTTPS and disabled only for loopback HTTP.
	http.SetCookie(w, &http.Cookie{
		Name: adminSessionCookie, Value: session.Token, Path: "/", HttpOnly: true,
		Secure: requestIsHTTPS(r), SameSite: http.SameSiteStrictMode, Expires: session.ExpiresAt,
	})
}

type authPageData struct {
	Mode        string
	Title       string
	Description string
	SubmitLabel string
	Username    string
	CSRFToken   string
	FormError   string
}

func (p *AdminPortal) render(w http.ResponseWriter, status int, data authPageData) {
	data.CSRFToken = p.auth.CSRFToken()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := authTemplate.Execute(w, data); err != nil {
		slog.Error("render management authentication", "error", err)
	}
}

func (p *AdminPortal) setupRequestAllowed(r *http.Request) bool {
	remote := strings.TrimSpace(r.RemoteAddr)
	if parsed, _, err := net.SplitHostPort(remote); err == nil {
		remote = parsed
	}
	if !isLoopbackHost(remote) && !p.trustSetupProxy {
		return false
	}
	host := strings.TrimSpace(r.Host)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	} else {
		host = strings.Trim(host, "[]")
	}
	return isLoopbackHost(host)
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), schemeHTTPS)
}
