package authhttp_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/usefabrin/fabrin/auth"
	"github.com/usefabrin/fabrin/authhttp"
	"github.com/usefabrin/fabrin/mail"
)

const browserOrigin = "https://app.example"

func TestBrowser_LoginCurrentAndLogout(t *testing.T) {
	router, inbox := browserRouter(t)
	denied := browserRequest(t, router, http.MethodGet, "/protected", "", "", "", nil)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("unprotected cookie route: status=%d body=%s", denied.Code, denied.Body.String())
	}
	bootstrap := browserRequest(t, router, http.MethodGet, "/bootstrap", "", browserOrigin, "", nil)
	if bootstrap.Code != http.StatusOK || bootstrap.Header().Get("Cache-Control") != "no-store" || bootstrap.Header().Get("Access-Control-Allow-Origin") != browserOrigin || bootstrap.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("bootstrap: status=%d headers=%v body=%s", bootstrap.Code, bootstrap.Header(), bootstrap.Body.String())
	}
	var boot struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeResponse(t, bootstrap, &boot)
	preAuth := responseCookie(t, bootstrap, "__Host-fabrin_preauth")
	assertSecureCookie(t, preAuth)

	request := browserRequest(t, router, http.MethodPost, "/request", `{"email":"Alice@EXAMPLE.COM"}`, browserOrigin, boot.CSRFToken, preAuth)
	if request.Code != http.StatusAccepted {
		t.Fatalf("request: status=%d body=%s", request.Code, request.Body.String())
	}
	var challenge struct {
		ID string `json:"challenge_id"`
	}
	decodeResponse(t, request, &challenge)
	code := messageCode(t, inbox.Drain())

	verify := browserRequest(t, router, http.MethodPost, "/verify", `{"challenge_id":"`+challenge.ID+`","email":"Alice@EXAMPLE.COM","code":"`+code+`"}`, browserOrigin, boot.CSRFToken, preAuth)
	if verify.Code != http.StatusOK {
		t.Fatalf("verify: status=%d body=%s", verify.Code, verify.Body.String())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(verify.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["token"]; exists {
		t.Fatalf("verify returned session credential: %s", verify.Body.String())
	}
	var login struct {
		CSRFToken string `json:"csrf_token"`
		Identity  struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"identity"`
	}
	decodeResponse(t, verify, &login)
	if login.CSRFToken == "" || login.CSRFToken == boot.CSRFToken || login.Identity.ID == "" || login.Identity.Email != "Alice@example.com" {
		t.Fatalf("login: %+v", login)
	}
	session := responseCookie(t, verify, "__Host-fabrin_session")
	assertSecureCookie(t, session)
	cleared := responseCookie(t, verify, "__Host-fabrin_preauth")
	if cleared.MaxAge >= 0 {
		t.Fatalf("pre-auth cookie was not cleared: %+v", cleared)
	}

	current := browserRequest(t, router, http.MethodGet, "/current", "", "", "", session)
	if current.Code != http.StatusOK {
		t.Fatalf("current: status=%d body=%s", current.Code, current.Body.String())
	}
	protected := browserRequest(t, router, http.MethodGet, "/protected", "", "", "", session)
	if protected.Code != http.StatusOK || !strings.Contains(protected.Body.String(), login.Identity.ID) {
		t.Fatalf("protected: status=%d body=%s", protected.Code, protected.Body.String())
	}
	unsafe := browserRequest(t, router, http.MethodPost, "/protected", "", browserOrigin, login.CSRFToken, session)
	if unsafe.Code != http.StatusNoContent {
		t.Fatalf("unsafe protected: status=%d body=%s", unsafe.Code, unsafe.Body.String())
	}
	missingOrigin := browserRequest(t, router, http.MethodPost, "/logout", "", "", login.CSRFToken, session)
	if missingOrigin.Code != http.StatusForbidden {
		t.Fatalf("missing origin: status=%d body=%s", missingOrigin.Code, missingOrigin.Body.String())
	}
	wrongCSRF := browserRequest(t, router, http.MethodPost, "/logout", "", browserOrigin, "wrong", session)
	if wrongCSRF.Code != http.StatusForbidden {
		t.Fatalf("wrong csrf: status=%d body=%s", wrongCSRF.Code, wrongCSRF.Body.String())
	}
	logout := browserRequest(t, router, http.MethodPost, "/logout", "", browserOrigin, login.CSRFToken, session)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout: status=%d body=%s", logout.Code, logout.Body.String())
	}
	revoked := browserRequest(t, router, http.MethodGet, "/current", "", "", "", session)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked: status=%d body=%s", revoked.Code, revoked.Body.String())
	}
}

func TestBrowser_RejectsUntrustedOriginsAndCrossBrowserVerification(t *testing.T) {
	router, inbox := browserRouter(t)
	for _, origin := range []string{"https://evil.example", "null"} {
		untrusted := browserRequest(t, router, http.MethodGet, "/bootstrap", "", origin, "", nil)
		if untrusted.Code != http.StatusForbidden || untrusted.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("untrusted bootstrap %q: status=%d headers=%v", origin, untrusted.Code, untrusted.Header())
		}
	}
	preflight := browserRequest(t, router, http.MethodOptions, "/request", "", browserOrigin, "", nil)
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Origin") != browserOrigin {
		t.Fatalf("preflight: status=%d headers=%v", preflight.Code, preflight.Header())
	}

	first, firstCSRF := bootstrapBrowser(t, router)
	second, secondCSRF := bootstrapBrowser(t, router)
	request := browserRequest(t, router, http.MethodPost, "/request", `{"email":"a@example.com"}`, browserOrigin, firstCSRF, first)
	var challenge struct {
		ID string `json:"challenge_id"`
	}
	decodeResponse(t, request, &challenge)
	code := messageCode(t, inbox.Drain())
	body := `{"challenge_id":"` + challenge.ID + `","email":"a@example.com","code":"` + code + `"}`
	wrongBrowser := browserRequest(t, router, http.MethodPost, "/verify", body, browserOrigin, secondCSRF, second)
	if wrongBrowser.Code != http.StatusUnauthorized {
		t.Fatalf("cross-browser verification: status=%d body=%s", wrongBrowser.Code, wrongBrowser.Body.String())
	}
	correctBrowser := browserRequest(t, router, http.MethodPost, "/verify", body, browserOrigin, firstCSRF, first)
	if correctBrowser.Code != http.StatusOK {
		t.Fatalf("correct browser: status=%d body=%s", correctBrowser.Code, correctBrowser.Body.String())
	}
}

func TestBrowser_StrictInputAndAmbiguousCookiesFailClosed(t *testing.T) {
	router, _ := browserRouter(t)
	preAuth, csrf := bootstrapBrowser(t, router)
	for _, body := range []string{
		`{"email":"a@example.com","admin":true}`,
		`{"email":"a@example.com"}{}`,
		`{"email":"` + strings.Repeat("a", 5000) + `"}`,
	} {
		response := browserRequest(t, router, http.MethodPost, "/request", body, browserOrigin, csrf, preAuth)
		if response.Code != http.StatusBadRequest || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("strict body: status=%d body=%s", response.Code, response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/request", strings.NewReader(`{"email":"a@example.com"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", browserOrigin)
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(preAuth)
	request.AddCookie(preAuth)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("duplicate pre-auth cookies: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNewBrowser_RestrictsOriginsAndInsecureCookiesToLoopback(t *testing.T) {
	store, _ := auth.NewMemoryStore(32)
	inbox, _ := mail.NewCapture(4)
	service, _ := auth.New(store, inbox, "test-key", bytes.Repeat([]byte{9}, 32))
	sessions, _ := auth.NewSessionManager(store)
	preAuth, _ := auth.NewPreAuthManager(store)
	for _, origin := range []string{"", "*", "null", "http://app.example", "https://app.example/path"} {
		if _, err := authhttp.NewBrowser(service, sessions, preAuth, []string{origin}); err == nil {
			t.Fatalf("accepted production origin %q", origin)
		}
	}
	for _, origin := range []string{"http://app.example", "http://localhost.evil"} {
		if _, err := authhttp.NewBrowser(service, sessions, preAuth, []string{origin}, authhttp.WithInsecureLoopback()); err == nil {
			t.Fatalf("accepted insecure non-loopback origin %q", origin)
		}
	}
	browser, err := authhttp.NewBrowser(service, sessions, preAuth, []string{"http://127.0.0.1:3000"}, authhttp.WithInsecureLoopback())
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/bootstrap", browser.Bootstrap)
	response := browserRequest(t, router, http.MethodGet, "/bootstrap", "", "", "", nil)
	cookie := responseCookie(t, response, "fabrin_dev_preauth")
	if cookie.Secure || !cookie.HttpOnly || strings.HasPrefix(cookie.Name, "__Host-") {
		t.Fatalf("invalid development cookie: %+v", cookie)
	}
}

func browserRouter(t *testing.T) (*gin.Engine, *mail.Capture) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, _ := auth.NewMemoryStore(256)
	inbox, _ := mail.NewCapture(32)
	service, err := auth.New(store, inbox, "test-key", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sessions, _ := auth.NewSessionManager(store)
	preAuth, _ := auth.NewPreAuthManager(store)
	browser, err := authhttp.NewBrowser(service, sessions, preAuth, []string{browserOrigin})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(browser.CORS())
	router.GET("/bootstrap", browser.Bootstrap)
	router.POST("/request", browser.RequestCode)
	router.POST("/verify", browser.VerifyCode)
	router.GET("/current", browser.Current)
	router.POST("/logout", browser.Logout)
	router.GET("/protected", browser.RequireAuth(), func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if !ok {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.JSON(http.StatusOK, gin.H{"identity_id": identity.ID})
	})
	router.POST("/protected", browser.RequireAuth(), browser.RequireCSRF(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.OPTIONS("/request", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return router, inbox
}

func bootstrapBrowser(t *testing.T, router http.Handler) (*http.Cookie, string) {
	t.Helper()
	response := browserRequest(t, router, http.MethodGet, "/bootstrap", "", browserOrigin, "", nil)
	var body struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeResponse(t, response, &body)
	return responseCookie(t, response, "__Host-fabrin_preauth"), body.CSRFToken
}

func browserRequest(t *testing.T, handler http.Handler, method, target, body, origin, csrf string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if method == http.MethodOptions {
		request.Header.Set("Access-Control-Request-Method", http.MethodPost)
		request.Header.Set("Access-Control-Request-Headers", "content-type, x-csrf-token")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func responseCookie(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing cookie %q: %v", name, response.Header().Values("Set-Cookie"))
	return nil
}

func assertSecureCookie(t *testing.T, cookie *http.Cookie) {
	t.Helper()
	if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge <= 0 || cookie.Domain != "" {
		t.Fatalf("unsafe cookie: %+v", cookie)
	}
}
