package authhttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/usefabrin/fabrin/auth"
)

const (
	preAuthCookieName = "__Host-fabrin_preauth"
	sessionCookieName = "__Host-fabrin_session"
	csrfHeaderName    = "X-CSRF-Token"
)

// Browser exposes email-code authentication through secure host-only cookies.
type Browser struct {
	service       *auth.Service
	sessions      *auth.SessionManager
	preAuth       *auth.PreAuthManager
	origins       map[string]struct{}
	source        func(*gin.Context) string
	secure        bool
	preAuthCookie string
	sessionCookie string
}

// BrowserOption configures browser authentication handlers.
type BrowserOption func(*browserSettings)

type browserSettings struct {
	source           func(*gin.Context) string
	insecureLoopback bool
}

// WithBrowserSource selects a trusted, bounded abuse-budget key. The default
// uses the direct peer address and ignores forwarding headers.
func WithBrowserSource(source func(*gin.Context) string) BrowserOption {
	return func(settings *browserSettings) { settings.source = source }
}

// WithInsecureLoopback permits HTTP origins only on loopback interfaces and
// switches to visibly development-only cookie names without the Secure flag.
func WithInsecureLoopback() BrowserOption {
	return func(settings *browserSettings) { settings.insecureLoopback = true }
}

// NewBrowser constructs browser authentication handlers for exact HTTPS
// origins. At least one origin is required; wildcards and opaque origins fail.
func NewBrowser(service *auth.Service, sessions *auth.SessionManager, preAuth *auth.PreAuthManager, allowedOrigins []string, options ...BrowserOption) (*Browser, error) {
	if service == nil || sessions == nil || preAuth == nil {
		return nil, errors.New("authhttp: service, session manager and pre-auth manager are required")
	}
	settings := browserSettings{source: directPeer}
	for i, option := range options {
		if option == nil {
			return nil, fmt.Errorf("authhttp: browser option %d is nil", i)
		}
		option(&settings)
	}
	if settings.source == nil {
		return nil, errors.New("authhttp: browser source function is required")
	}
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if !validOrigin(origin, settings.insecureLoopback) {
			return nil, fmt.Errorf("authhttp: invalid browser origin %q", origin)
		}
		origins[origin] = struct{}{}
	}
	if len(origins) == 0 {
		return nil, errors.New("authhttp: at least one browser origin is required")
	}
	browser := &Browser{service: service, sessions: sessions, preAuth: preAuth, origins: origins, source: settings.source, secure: !settings.insecureLoopback}
	if browser.secure {
		browser.preAuthCookie = preAuthCookieName
		browser.sessionCookie = sessionCookieName
	} else {
		browser.preAuthCookie = "fabrin_dev_preauth"
		browser.sessionCookie = "fabrin_dev_session"
	}
	return browser, nil
}

// CORS returns exact-origin credentialed CORS middleware for browser auth
// routes. Unsafe handlers repeat the origin check so omitting this middleware
// cannot disable CSRF protection.
func (b *Browser) CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			if c.Request.Method == http.MethodOptions {
				noStore(c)
				writeError(c, http.StatusForbidden, "origin_denied")
				return
			}
			c.Next()
			return
		}
		if !b.allowedOrigin(origin) {
			noStore(c)
			writeError(c, http.StatusForbidden, "origin_denied")
			return
		}
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Add("Vary", "Origin")
		if c.Request.Method != http.MethodOptions {
			c.Next()
			return
		}
		noStore(c)
		if !validPreflight(c.GetHeader("Access-Control-Request-Method"), c.GetHeader("Access-Control-Request-Headers")) {
			writeError(c, http.StatusForbidden, "origin_denied")
			return
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST")
		c.Header("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token")
		c.Header("Access-Control-Max-Age", "600")
		c.Writer.Header().Add("Vary", "Access-Control-Request-Method")
		c.Writer.Header().Add("Vary", "Access-Control-Request-Headers")
		c.AbortWithStatus(http.StatusNoContent)
	}
}

// Bootstrap creates bounded pre-authentication state, sets its credential in a
// secure cookie and returns only the independent CSRF token.
func (b *Browser) Bootstrap(c *gin.Context) {
	noStore(c)
	if !b.safeOrigin(c) {
		return
	}
	state, err := b.preAuth.Bootstrap(c.Request.Context(), b.source(c))
	if err != nil {
		if errors.Is(err, auth.ErrRateLimited) {
			writeError(c, http.StatusTooManyRequests, "rate_limited")
		} else {
			writeError(c, http.StatusServiceUnavailable, "temporarily_unavailable")
		}
		return
	}
	setBrowserCookie(c, b.preAuthCookie, state.Credential, state.ExpiresAt, b.secure)
	c.JSON(http.StatusOK, gin.H{"csrf_token": state.CSRFToken, "expires_at": state.ExpiresAt})
}

// RequestCode validates pre-authentication CSRF state and requests a
// browser-purpose email challenge bound to that state.
func (b *Browser) RequestCode(c *gin.Context) {
	noStore(c)
	credential, _, ok := b.preAuthRequest(c)
	if !ok {
		return
	}
	var request struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	challenge, err := b.service.Request(c.Request.Context(), request.Email, auth.PurposeBrowser, b.source(c), auth.WithBinding(credential))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrAuthentication):
			writeError(c, http.StatusBadRequest, "invalid_request")
			return
		case errors.Is(err, auth.ErrUnavailable):
			writeError(c, http.StatusServiceUnavailable, "temporarily_unavailable")
			return
		case errors.Is(err, auth.ErrDelivery), errors.Is(err, auth.ErrRateLimited):
			challenge, err = neutralChallenge()
			if err != nil {
				writeError(c, http.StatusServiceUnavailable, "temporarily_unavailable")
				return
			}
		default:
			writeError(c, http.StatusServiceUnavailable, "temporarily_unavailable")
			return
		}
	}
	c.JSON(http.StatusAccepted, gin.H{"challenge_id": challenge.ID, "expires_at": challenge.ExpiresAt})
}

// VerifyCode consumes a browser-purpose challenge and replaces pre-auth state
// with a fresh cookie session and CSRF token.
func (b *Browser) VerifyCode(c *gin.Context) {
	noStore(c)
	credential, csrf, ok := b.preAuthRequest(c)
	if !ok {
		return
	}
	var request struct {
		ChallengeID string `json:"challenge_id"`
		Email       string `json:"email"`
		Code        string `json:"code"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := b.service.Verify(c.Request.Context(), request.ChallengeID, request.Email, request.Code, auth.PurposeBrowser, b.source(c), auth.WithBinding(credential))
	if err != nil {
		writeAuthError(c, err)
		return
	}
	if err := b.preAuth.Consume(c.Request.Context(), credential, csrf); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
		defer cancel()
		_ = b.sessions.Logout(cleanupCtx, result.Session.Credential)
		if errors.Is(err, auth.ErrPreAuth) {
			writeError(c, http.StatusUnauthorized, "authentication_failed")
		} else {
			writeError(c, http.StatusServiceUnavailable, "temporarily_unavailable")
		}
		return
	}
	clearBrowserCookie(c, b.preAuthCookie, b.secure)
	setBrowserCookie(c, b.sessionCookie, result.Session.Credential, result.Session.ExpiresAt, b.secure)
	c.JSON(http.StatusOK, gin.H{"identity": identityJSON(result.Identity), "csrf_token": result.Session.CSRFToken, "expires_at": result.Session.ExpiresAt})
}

// Current authenticates the browser session cookie and refreshes idle activity.
func (b *Browser) Current(c *gin.Context) {
	noStore(c)
	credential, ok := exactCookie(c.Request, b.sessionCookie)
	if !ok {
		writeError(c, http.StatusUnauthorized, "authentication_failed")
		return
	}
	identity, err := b.sessions.Current(c.Request.Context(), credential)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"identity": identityJSON(identity)})
}

// Logout validates origin and CSRF state, revokes the current session and
// clears its cookie.
func (b *Browser) Logout(c *gin.Context) {
	b.logout(c, false)
}

// LogoutAll validates origin and CSRF state, then revokes every session for
// the authenticated identity and clears the current cookie.
func (b *Browser) LogoutAll(c *gin.Context) {
	b.logout(c, true)
}

func (b *Browser) logout(c *gin.Context, all bool) {
	noStore(c)
	if !b.unsafeOrigin(c) {
		return
	}
	credential, ok := exactCookie(c.Request, b.sessionCookie)
	if !ok {
		writeError(c, http.StatusUnauthorized, "authentication_failed")
		return
	}
	csrf, ok := exactHeader(c.Request.Header, csrfHeaderName)
	if !ok || b.sessions.ValidateCSRF(c.Request.Context(), credential, csrf) != nil {
		writeError(c, http.StatusForbidden, "csrf_failed")
		return
	}
	var err error
	if all {
		err = b.sessions.LogoutAll(c.Request.Context(), credential)
	} else {
		err = b.sessions.Logout(c.Request.Context(), credential)
	}
	clearBrowserCookie(c, b.sessionCookie, b.secure)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (b *Browser) preAuthRequest(c *gin.Context) (string, string, bool) {
	if !b.unsafeOrigin(c) {
		return "", "", false
	}
	credential, cookieOK := exactCookie(c.Request, b.preAuthCookie)
	csrf, headerOK := exactHeader(c.Request.Header, csrfHeaderName)
	if !cookieOK || !headerOK {
		writeError(c, http.StatusForbidden, "csrf_failed")
		return "", "", false
	}
	if err := b.preAuth.Authenticate(c.Request.Context(), credential, csrf); err != nil {
		if errors.Is(err, auth.ErrUnavailable) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeError(c, http.StatusServiceUnavailable, "temporarily_unavailable")
		} else {
			writeError(c, http.StatusForbidden, "csrf_failed")
		}
		return "", "", false
	}
	return credential, csrf, true
}

func (b *Browser) safeOrigin(c *gin.Context) bool {
	origin := c.GetHeader("Origin")
	if origin == "" || b.allowedOrigin(origin) {
		return true
	}
	writeError(c, http.StatusForbidden, "origin_denied")
	return false
}

func (b *Browser) unsafeOrigin(c *gin.Context) bool {
	if b.allowedOrigin(c.GetHeader("Origin")) {
		return true
	}
	writeError(c, http.StatusForbidden, "origin_denied")
	return false
}

func (b *Browser) allowedOrigin(origin string) bool {
	_, ok := b.origins[origin]
	return ok
}

func validOrigin(origin string, insecureLoopback bool) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || origin == "null" || strings.Contains(origin, "*") {
		return false
	}
	if !insecureLoopback {
		return parsed.Scheme == "https"
	}
	if parsed.Scheme != "http" {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func validPreflight(method, headers string) bool {
	if method != http.MethodGet && method != http.MethodPost {
		return false
	}
	for _, header := range strings.Split(headers, ",") {
		header = strings.TrimSpace(header)
		if header != "" && !strings.EqualFold(header, "content-type") && !strings.EqualFold(header, csrfHeaderName) {
			return false
		}
	}
	return true
}

func exactCookie(request *http.Request, name string) (string, bool) {
	value := ""
	count := 0
	for _, cookie := range request.Cookies() {
		if cookie.Name == name {
			value = cookie.Value
			count++
		}
	}
	return value, count == 1 && value != "" && len(value) <= 256
}

func exactHeader(header http.Header, name string) (string, bool) {
	values := header.Values(name)
	returnValue := ""
	if len(values) == 1 {
		returnValue = values[0]
	}
	return returnValue, len(values) == 1 && returnValue != "" && len(returnValue) <= 256 && !strings.ContainsAny(returnValue, " \t\r\n,")
}

func setBrowserCookie(c *gin.Context, name, value string, expires time.Time, secure bool) {
	maxAge := int(time.Until(expires).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: value, Path: "/", Expires: expires, MaxAge: maxAge, Secure: secure, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func clearBrowserCookie(c *gin.Context, name string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{Name: name, Path: "/", Expires: time.Unix(1, 0).UTC(), MaxAge: -1, Secure: secure, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}
