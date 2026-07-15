package core

import (
	"bufio"
	"context"
	crypto "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

func getRequestIp(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.Split(xff, ",")[0]
	}

	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return xrip
	}

	return r.RemoteAddr
}

type logResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *logResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *logResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(http.Hijacker).Hijack()
}

var _ http.Hijacker = (*logResponseWriter)(nil)

const (
	ContextKeyRequestId = "requestId"
	ContextKeyLogger    = "logger"
)

func getRequestId(r *http.Request) string {
	return r.Context().Value(ContextKeyRequestId).(string)
}

func getRequestLogger(r *http.Request) *slog.Logger {
	return r.Context().Value(ContextKeyLogger).(*slog.Logger)
}

func requestLogger(next http.Handler) http.Handler {
	requestId := func() string {
		d := make([]byte, 8)
		crypto.Read(d)
		return hex.EncodeToString(d)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &logResponseWriter{w, http.StatusOK}
		start := time.Now()

		rid := requestId()
		w.Header().Set("X-Request-Id", rid)

		rslog := slog.With(
			"request_id", rid,
			"method", r.Method,
			"path", r.URL.Path,
		)

		ctx := context.WithValue(r.Context(), ContextKeyRequestId, rid)
		ctx = context.WithValue(ctx, ContextKeyLogger, rslog)

		rslog.Info(
			"request incoming",
			"remote_addr", getRequestIp(r),
		)
		next.ServeHTTP(rw, r.WithContext(ctx))
		rslog.Info(
			"request complete",
			"status", rw.status,
			"duration", time.Since(start),
		)
	})
}

func httpErrorInternal(r *http.Request, w http.ResponseWriter, err error, msg string) {
	rslog := getRequestLogger(r)
	rslog.Error(msg, "error", err)
	http.Error(w, "Error: internal server error", http.StatusInternalServerError)
}

type Config struct {
	Port         uint16
	LobbiesCount int

	// env
	DbUrl string

	ClientRedirectUrl  string
	Domain             string
	Prod               bool
	GoogleClientId     string
	GoogleClientSecret string
}

func (c *Config) Validate() error {
	if c.Port == 0 {
		return errors.New("port must be set")
	}
	if c.LobbiesCount == 0 {
		return errors.New("lobbies count must be set")
	}
	if c.DbUrl == "" {
		return errors.New("db url must be set")
	}
	if c.ClientRedirectUrl == "" {
		return errors.New("client redirect url must be set")
	}
	if c.Domain == "" {
		return errors.New("domain must be set")
	}
	if c.GoogleClientId == "" {
		return errors.New("google client id must be set")
	}
	if c.GoogleClientSecret == "" {
		return errors.New("google client secret must be set")
	}
	return nil
}

func Run(config *Config) error {
	if err := config.Validate(); err != nil {
		return err
	}

	// db
	dbConn, err := pgxpool.New(context.Background(), config.DbUrl)
	if err != nil {
		return fmt.Errorf("unable to connect to db: %w", err)
	}
	slog.Info("DB connected")

	// auth
	authConfig := &oauth2.Config{
		ClientID:     config.GoogleClientId,
		ClientSecret: config.GoogleClientSecret,
		Scopes:       []string{"openid", "profile", "email"},
		Endpoint:     google.Endpoint,
	}
	if config.Prod {
		authConfig.RedirectURL = fmt.Sprintf("https://%s/api/callback", config.Domain)
	} else {
		authConfig.RedirectURL = fmt.Sprintf("http://%s:%d/api/callback", config.Domain, config.Port)
	}
	slog.Info("auth config loaded", "redirect_url", authConfig.RedirectURL)

	states := newStateStore()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth", authHandler(dbConn, states, config.ClientRedirectUrl))
	mux.HandleFunc("/api/callback", callbackHandler(dbConn, states, config.ClientRedirectUrl, config.Domain, config.Prod))
	mux.HandleFunc("/api/logout", authLogoutHandler(dbConn, config.ClientRedirectUrl, config.Domain, config.Prod))
	mux.HandleFunc("/api/game", gameHandler(config.Prod, dbConn, config.LobbiesCount))

	handler := requestLogger(mux)

	slog.Info(fmt.Sprintf("listening at http://%s:%d", config.Domain, config.Port))
	if err := http.ListenAndServe(fmt.Sprintf(":%d", config.Port), handler); err != nil {
		return fmt.Errorf("unable to listen: %w", err)
	}

	return nil
}
