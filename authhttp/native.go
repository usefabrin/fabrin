// Package authhttp provides bounded HTTP handlers for Fabrin authentication.
package authhttp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/usefabrin/fabrin/auth"
)

const maxJSONBody = 4096

// Native exposes email-code authentication for clients that explicitly use
// bearer credentials. Its handlers never read or set authentication cookies.
type Native struct {
	service  *auth.Service
	sessions *auth.SessionManager
	source   func(*gin.Context) string
}

// NativeOption configures native authentication handlers.
type NativeOption func(*nativeSettings)

type nativeSettings struct {
	source func(*gin.Context) string
}

// WithSource selects a trusted, bounded abuse-budget key for a request. The
// default uses the direct peer address and ignores forwarding headers.
func WithSource(source func(*gin.Context) string) NativeOption {
	return func(settings *nativeSettings) { settings.source = source }
}

// NewNative constructs native authentication handlers.
func NewNative(service *auth.Service, sessions *auth.SessionManager, options ...NativeOption) (*Native, error) {
	if service == nil || sessions == nil {
		return nil, errors.New("authhttp: service and session manager are required")
	}
	settings := nativeSettings{source: directPeer}
	for i, option := range options {
		if option == nil {
			return nil, fmt.Errorf("authhttp: option %d is nil", i)
		}
		option(&settings)
	}
	if settings.source == nil {
		return nil, errors.New("authhttp: source function is required")
	}
	return &Native{service: service, sessions: sessions, source: settings.source}, nil
}

// RequestCode accepts a strict email request and always suppresses known
// delivery and address-budget outcomes behind the same accepted response.
func (n *Native) RequestCode(c *gin.Context) {
	noStore(c)
	var request struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	challenge, err := n.service.Request(c.Request.Context(), request.Email, auth.PurposeNative, n.source(c))
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

// VerifyCode consumes a native-purpose challenge and returns its opaque bearer
// credential once. The credential is never written to a cookie.
func (n *Native) VerifyCode(c *gin.Context) {
	noStore(c)
	var request struct {
		ChallengeID string `json:"challenge_id"`
		Email       string `json:"email"`
		Code        string `json:"code"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := n.service.Verify(c.Request.Context(), request.ChallengeID, request.Email, request.Code, auth.PurposeNative, n.source(c))
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"identity":   identityJSON(result.Identity),
		"token":      result.Session.Credential,
		"expires_at": result.Session.ExpiresAt,
	})
}

// Current authenticates the Authorization bearer credential and refreshes its
// server-side idle deadline.
func (n *Native) Current(c *gin.Context) {
	noStore(c)
	credential, ok := bearerCredential(c.Request)
	if !ok {
		writeError(c, http.StatusUnauthorized, "authentication_failed")
		return
	}
	identity, err := n.sessions.Current(c.Request.Context(), credential)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"identity": identityJSON(identity)})
}

// Logout revokes the Authorization bearer credential before returning success.
func (n *Native) Logout(c *gin.Context) {
	noStore(c)
	credential, ok := bearerCredential(c.Request)
	if !ok {
		writeError(c, http.StatusUnauthorized, "authentication_failed")
		return
	}
	if err := n.sessions.Logout(c.Request.Context(), credential); err != nil {
		writeAuthError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func decodeJSON(c *gin.Context, target any) error {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("invalid content type")
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxJSONBody)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func bearerCredential(request *http.Request) (string, bool) {
	values := request.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return "", false
	}
	credential := strings.TrimPrefix(values[0], "Bearer ")
	return credential, credential != "" && len(credential) <= 256 && !strings.ContainsAny(credential, " \t\r\n")
}

func directPeer(c *gin.Context) string {
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		return ""
	}
	return host
}

func neutralChallenge() (auth.Challenge, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return auth.Challenge{}, err
	}
	return auth.Challenge{ID: hex.EncodeToString(value), ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}, nil
}

func noStore(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
}

func writeAuthError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrRateLimited):
		writeError(c, http.StatusTooManyRequests, "rate_limited")
	case errors.Is(err, auth.ErrUnavailable), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeError(c, http.StatusServiceUnavailable, "temporarily_unavailable")
	default:
		writeError(c, http.StatusUnauthorized, "authentication_failed")
	}
}

func writeError(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"error": code})
}

func identityJSON(identity auth.Identity) gin.H {
	return gin.H{"id": identity.ID, "email": identity.Email, "created_at": identity.CreatedAt}
}
