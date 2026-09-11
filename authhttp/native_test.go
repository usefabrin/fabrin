package authhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/usefabrin/fabrin/auth"
	"github.com/usefabrin/fabrin/authhttp"
	"github.com/usefabrin/fabrin/mail"
)

func TestNative_LoginCurrentAndLogout(t *testing.T) {
	router, inbox, service := nativeRouter(t, nil)
	denied := perform(t, router, http.MethodGet, "/protected", "", "")
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("unprotected bearer route: status=%d body=%s", denied.Code, denied.Body.String())
	}

	request := perform(t, router, http.MethodPost, "/request", `{"email":"Alice@EXAMPLE.COM"}`, "")
	if request.Code != http.StatusAccepted || request.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("request: status=%d cache=%q body=%s", request.Code, request.Header().Get("Cache-Control"), request.Body.String())
	}
	var challenge struct {
		ID string `json:"challenge_id"`
	}
	decodeResponse(t, request, &challenge)
	code := messageCode(t, inbox.Drain())

	browserChallenge, err := service.Request(t.Context(), "alice@example.com", auth.PurposeBrowser, "browser-test")
	if err != nil {
		t.Fatal(err)
	}
	browserCode := messageCode(t, inbox.Drain())
	wrongPurpose := perform(t, router, http.MethodPost, "/verify", `{"challenge_id":"`+browserChallenge.ID+`","email":"alice@example.com","code":"`+browserCode+`"}`, "")
	if wrongPurpose.Code != http.StatusUnauthorized {
		t.Fatalf("browser challenge accepted by native route: %d %s", wrongPurpose.Code, wrongPurpose.Body.String())
	}

	verify := perform(t, router, http.MethodPost, "/verify", `{"challenge_id":"`+challenge.ID+`","email":"Alice@EXAMPLE.COM","code":"`+code+`"}`, "")
	if verify.Code != http.StatusOK || verify.Header().Get("Cache-Control") != "no-store" || len(verify.Result().Cookies()) != 0 {
		t.Fatalf("verify: status=%d cache=%q cookies=%v body=%s", verify.Code, verify.Header().Get("Cache-Control"), verify.Result().Cookies(), verify.Body.String())
	}
	var login struct {
		Token    string `json:"token"`
		Identity struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"identity"`
	}
	decodeResponse(t, verify, &login)
	if login.Token == "" || login.Identity.ID == "" || login.Identity.Email != "Alice@example.com" {
		t.Fatalf("login response: %+v", login)
	}

	queryToken := perform(t, router, http.MethodGet, "/current?access_token="+login.Token, "", "")
	if queryToken.Code != http.StatusUnauthorized {
		t.Fatalf("query token accepted: %d", queryToken.Code)
	}
	current := perform(t, router, http.MethodGet, "/current", "", "Bearer "+login.Token)
	if current.Code != http.StatusOK || current.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("current: status=%d body=%s", current.Code, current.Body.String())
	}
	protected := perform(t, router, http.MethodGet, "/protected", "", "Bearer "+login.Token)
	if protected.Code != http.StatusOK || !strings.Contains(protected.Body.String(), login.Identity.ID) {
		t.Fatalf("protected: status=%d body=%s", protected.Code, protected.Body.String())
	}

	logout := perform(t, router, http.MethodPost, "/logout", "", "Bearer "+login.Token)
	if logout.Code != http.StatusNoContent || logout.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("logout: status=%d body=%s", logout.Code, logout.Body.String())
	}
	revoked := perform(t, router, http.MethodGet, "/current", "", "Bearer "+login.Token)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session accepted: %d", revoked.Code)
	}

	allRequest := perform(t, router, http.MethodPost, "/request", `{"email":"all@example.com"}`, "")
	var allChallenge struct {
		ID string `json:"challenge_id"`
	}
	decodeResponse(t, allRequest, &allChallenge)
	allCode := messageCode(t, inbox.Drain())
	allVerify := perform(t, router, http.MethodPost, "/verify", `{"challenge_id":"`+allChallenge.ID+`","email":"all@example.com","code":"`+allCode+`"}`, "")
	var allLogin struct {
		Token string `json:"token"`
	}
	decodeResponse(t, allVerify, &allLogin)
	logoutAll := perform(t, router, http.MethodPost, "/logout-all", "", "Bearer "+allLogin.Token)
	if logoutAll.Code != http.StatusNoContent {
		t.Fatalf("logout all: status=%d body=%s", logoutAll.Code, logoutAll.Body.String())
	}
}

func TestNative_StrictBodiesAndNeutralDeliveryResponse(t *testing.T) {
	router, _, _ := nativeRouter(t, nil)
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "unknown field", contentType: "application/json", body: `{"email":"a@example.com","admin":true}`},
		{name: "trailing value", contentType: "application/json", body: `{"email":"a@example.com"}{}`},
		{name: "wrong content type", contentType: "text/plain", body: `{"email":"a@example.com"}`},
		{name: "oversize", contentType: "application/json", body: `{"email":"` + strings.Repeat("a", 5000) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := performContentType(t, router, http.MethodPost, "/request", tc.body, "", tc.contentType)
			if response.Code != http.StatusBadRequest || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
			}
		})
	}

	failingRouter, _, _ := nativeRouter(t, failingSender{})
	failed := perform(t, failingRouter, http.MethodPost, "/request", `{"email":"eligible@example.com"}`, "")
	if failed.Code != http.StatusAccepted {
		t.Fatalf("delivery outcome leaked status: %d %s", failed.Code, failed.Body.String())
	}
	var response struct {
		ID string `json:"challenge_id"`
	}
	decodeResponse(t, failed, &response)
	if len(response.ID) != 64 {
		t.Fatalf("neutral challenge id length: %d", len(response.ID))
	}
}

func TestNative_DefaultSourceIgnoresForwardingHeaders(t *testing.T) {
	router, inbox, _ := nativeRouter(t, nil)
	for i := range 21 {
		request := httptest.NewRequest(http.MethodPost, "/request", strings.NewReader(`{"email":"source-`+fmt.Sprintf("%02d", i)+`@example.com"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("request %d: status=%d body=%s", i, response.Code, response.Body.String())
		}
	}
	if messages := inbox.Messages(); len(messages) != 20 {
		t.Fatalf("delivered messages after source limit: %d", len(messages))
	}
}

func nativeRouter(t *testing.T, sender auth.Sender) (*gin.Engine, *mail.Capture, *auth.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, err := auth.NewMemoryStore(256)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := mail.NewCapture(32)
	if err != nil {
		t.Fatal(err)
	}
	if sender == nil {
		sender = inbox
	}
	service, err := auth.New(store, sender, "test-key", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := auth.NewSessionManager(store)
	if err != nil {
		t.Fatal(err)
	}
	native, err := authhttp.NewNative(service, sessions)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/request", native.RequestCode)
	router.POST("/verify", native.VerifyCode)
	router.GET("/current", native.Current)
	router.POST("/logout", native.Logout)
	router.POST("/logout-all", native.LogoutAll)
	router.GET("/protected", native.RequireAuth(), func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if !ok {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.JSON(http.StatusOK, gin.H{"identity_id": identity.ID})
	})
	return router, inbox, service
}

func perform(t *testing.T, handler http.Handler, method, target, body, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	return performContentType(t, handler, method, target, body, authorization, "application/json")
}

func performContentType(t *testing.T, handler http.Handler, method, target, body, authorization, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v body=%s", err, response.Body.String())
	}
}

func messageCode(t *testing.T, messages []mail.Message) string {
	t.Helper()
	if len(messages) != 1 {
		t.Fatalf("captured messages: %d", len(messages))
	}
	const prefix = "Your Fabrin sign-in code is: "
	return messages[0].Text[len(prefix):]
}

type failingSender struct{}

func (failingSender) Send(context.Context, mail.Message) error {
	return errors.New("provider rejected")
}
