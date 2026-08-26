package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wellivea1/alexa-brainslug/internal/adminauth"
)

func newTestAdminPortal(t *testing.T) (*AdminPortal, *adminauth.Manager) {
	t.Helper()
	manager, err := adminauth.New(filepath.Join(t.TempDir(), "brainslug.admin-auth.json"), adminauth.Options{
		PasswordIterations: 1_000,
		MaxFailures:        3,
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewAdminPortal(manager), manager
}

func TestInitialSetupIsLoopbackOnlyAndCreatesProtectedOwner(t *testing.T) {
	portal, manager := newTestAdminPortal(t)

	remote := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/setup", http.NoBody)
	remote.Host = "admin.example:8081"
	remoteRec := httptest.NewRecorder()
	portal.HandleSetupGet(remoteRec, remote)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("remote setup status = %d", remoteRec.Code)
	}
	spoofedHost := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/setup", http.NoBody)
	spoofedHost.Host = "127.0.0.1:8081"
	spoofedRec := httptest.NewRecorder()
	portal.HandleSetupGet(spoofedRec, spoofedHost)
	if spoofedRec.Code != http.StatusForbidden {
		t.Fatalf("remote setup with spoofed Host status = %d", spoofedRec.Code)
	}

	local := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/setup", http.NoBody)
	local.Host = "127.0.0.1:8081"
	local.RemoteAddr = "127.0.0.1:1000"
	localRec := httptest.NewRecorder()
	portal.HandleSetupGet(localRec, local)
	if localRec.Code != http.StatusOK || !strings.Contains(localRec.Body.String(), "Create owner account") {
		t.Fatalf("local setup = %d %q", localRec.Code, localRec.Body.String())
	}

	mismatch := setupRequest(t, manager, "owner", "correct horse battery staple", "different password")
	mismatchRec := httptest.NewRecorder()
	portal.HandleSetupPost(mismatchRec, mismatch)
	if mismatchRec.Code != http.StatusBadRequest || !strings.Contains(mismatchRec.Body.String(), "Passwords do not match") {
		t.Fatalf("password mismatch = %d %q", mismatchRec.Code, mismatchRec.Body.String())
	}

	setup := setupRequest(t, manager, "owner", "correct horse battery staple", "correct horse battery staple")
	setup.Header.Set(dashboardFetchHeader, "fetch")
	setupRec := httptest.NewRecorder()
	portal.HandleSetupPost(setupRec, setup)
	if setupRec.Code != http.StatusOK || !strings.Contains(setupRec.Body.String(), `"location":"/?setup=complete"`) {
		t.Fatalf("setup response = %d %q", setupRec.Code, setupRec.Body.String())
	}
	cookies := setupRec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != adminSessionCookie || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %#v", cookies)
	}

	repeat := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/setup", http.NoBody)
	repeat.Host = "127.0.0.1:8081"
	repeat.RemoteAddr = "127.0.0.1:1000"
	repeatRec := httptest.NewRecorder()
	portal.HandleSetupGet(repeatRec, repeat)
	if repeatRec.Code != http.StatusSeeOther || repeatRec.Header().Get("Location") != "/login" {
		t.Fatalf("repeat setup = %d %q", repeatRec.Code, repeatRec.Header().Get("Location"))
	}
}

func TestLoginMiddlewareAndLogoutLifecycle(t *testing.T) {
	portal, manager := newTestAdminPortal(t)
	if _, err := manager.CreateOwner("owner", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}

	protected := portal.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := adminauth.UsernameFromContext(r.Context()); got != "owner" {
			t.Errorf("authenticated username = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	unauthenticated := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	unauthenticatedRec := httptest.NewRecorder()
	protected.ServeHTTP(unauthenticatedRec, unauthenticated)
	if unauthenticatedRec.Code != http.StatusSeeOther || unauthenticatedRec.Header().Get("Location") != "/login" {
		t.Fatalf("unauthenticated response = %d %q", unauthenticatedRec.Code, unauthenticatedRec.Header().Get("Location"))
	}

	bad := loginRequest(t, manager, "unknown", "wrong password")
	badRec := httptest.NewRecorder()
	portal.HandleLoginPost(badRec, bad)
	if badRec.Code != http.StatusUnauthorized || !strings.Contains(badRec.Body.String(), "Invalid username or password") {
		t.Fatalf("bad login = %d %q", badRec.Code, badRec.Body.String())
	}

	login := loginRequest(t, manager, "OWNER", "correct horse battery staple")
	login.Header.Set("X-Forwarded-Proto", "https")
	loginRec := httptest.NewRecorder()
	portal.HandleLoginPost(loginRec, login)
	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("secure login cookie = %#v", cookies)
	}

	authorized := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	authorized.AddCookie(cookies[0])
	authorizedRec := httptest.NewRecorder()
	protected.ServeHTTP(authorizedRec, authorized)
	if authorizedRec.Code != http.StatusNoContent {
		t.Fatalf("authorized status = %d", authorizedRec.Code)
	}

	logoutForm := url.Values{"csrf_token": {manager.CSRFToken()}}
	logout := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/logout", strings.NewReader(logoutForm.Encode()))
	logout.Host = "127.0.0.1:8081"
	logout.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logout.Header.Set("Origin", "http://127.0.0.1:8081")
	logout.AddCookie(cookies[0])
	logoutRec := httptest.NewRecorder()
	portal.HandleLogout(logoutRec, logout)
	if logoutRec.Code != http.StatusSeeOther || logoutRec.Header().Get("Location") != "/login" {
		t.Fatalf("logout = %d %q", logoutRec.Code, logoutRec.Header().Get("Location"))
	}

	afterLogout := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	afterLogout.AddCookie(cookies[0])
	afterLogoutRec := httptest.NewRecorder()
	protected.ServeHTTP(afterLogoutRec, afterLogout)
	if afterLogoutRec.Code != http.StatusSeeOther {
		t.Fatalf("logged-out session status = %d", afterLogoutRec.Code)
	}
}

func TestAuthenticationFormsRequireCSRFAndSameOrigin(t *testing.T) {
	portal, manager := newTestAdminPortal(t)
	missingToken := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/setup", strings.NewReader("username=owner"))
	missingToken.Host = "127.0.0.1:8081"
	missingToken.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	missingToken.Header.Set("Origin", "http://127.0.0.1:8081")
	missingRec := httptest.NewRecorder()
	portal.HandleSetupPost(missingRec, missingToken)
	if missingRec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", missingRec.Code)
	}

	crossOrigin := setupRequest(t, manager, "owner", "correct horse battery staple", "correct horse battery staple")
	crossOrigin.Header.Set("Origin", "https://example.com")
	crossRec := httptest.NewRecorder()
	portal.HandleSetupPost(crossRec, crossOrigin)
	if crossRec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin setup status = %d", crossRec.Code)
	}
}

func TestInitialSetupAllowsExplicitTrustedLocalProxy(t *testing.T) {
	_, manager := newTestAdminPortal(t)
	portal := NewAdminPortal(manager, AdminPortalOptions{TrustSetupProxy: true})
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/setup", http.NoBody)
	request.Host = "127.0.0.1:8081"
	request.RemoteAddr = "172.20.0.1:50000"
	recorder := httptest.NewRecorder()
	portal.HandleSetupGet(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("trusted proxy setup status = %d", recorder.Code)
	}
}

func setupRequest(t *testing.T, manager *adminauth.Manager, username, password, confirmation string) *http.Request {
	t.Helper()
	form := url.Values{
		"csrf_token":            {manager.CSRFToken()},
		"username":              {username},
		"password":              {password},
		"password_confirmation": {confirmation},
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/setup", strings.NewReader(form.Encode()))
	request.Host = "127.0.0.1:8081"
	request.RemoteAddr = "127.0.0.1:1000"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "http://127.0.0.1:8081")
	return request
}

func loginRequest(t *testing.T, manager *adminauth.Manager, username, password string) *http.Request {
	t.Helper()
	form := url.Values{"csrf_token": {manager.CSRFToken()}, "username": {username}, "password": {password}}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Host = "127.0.0.1:8081"
	request.RemoteAddr = "127.0.0.1:1000"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "http://127.0.0.1:8081")
	return request
}
