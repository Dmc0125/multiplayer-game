package main

import (
	"bufio"
	"context"
	crypto "crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
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

func logRequest(next http.Handler) http.Handler {
	requestId := func() string {
		d := make([]byte, 8)
		crypto.Read(d)
		return hex.EncodeToString(d)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// log method, path, status, and duration
		rw := &logResponseWriter{w, http.StatusOK}
		start := time.Now()

		rid := requestId()
		w.Header().Set("X-Request-Id", rid)
		ctx := context.WithValue(r.Context(), "requestId", rid)

		slog.Info(
			"request incoming",
			"request_id", rid,
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", getRequestIp(r),
			"user_agent", r.UserAgent(),
		)
		next.ServeHTTP(rw, r.WithContext(ctx))
		slog.Info(
			"request complete",
			"request_id", rid,
			"status", rw.status,
			"duration", time.Since(start),
		)
	})
}

func getRequestId(r *http.Request) string {
	return r.Context().Value("requestId").(string)
}

func getUserIdFromSession(dbConn *pgxpool.Pool, r *http.Request) (userId int, status int) {
	sessionId, _ := r.Cookie("session_id")
	if sessionId == nil {
		status = http.StatusUnauthorized
		return
	}

	status = http.StatusOK

	q := "select u.id from sessions s join users u on u.id = s.user_id where s.id = $1 and s.expires_at > now()"
	userId = -1
	err := dbConn.QueryRow(r.Context(), q, sessionId.Value).Scan(&userId)
	if errors.Is(err, pgx.ErrNoRows) {
		status = http.StatusUnauthorized
	} else if err != nil {
		logReqError(r, "unable to get session", "error", err)
		status = http.StatusInternalServerError
	}

	return
}

func logReqInfo(r *http.Request, msg string, args ...any) {
	a := []any{"request_id", getRequestId(r), "path", r.URL.Path}
	a = append(a, args...)
	slog.Info(msg, a...)
}

func logReqError(r *http.Request, msg string, args ...any) {
	a := []any{"request_id", getRequestId(r), "path", r.URL.Path}
	a = append(a, args...)
	slog.Error(msg, a...)
}

func loadEnvVariable(key string, required bool) (value string) {
	value = os.Getenv(key)
	if value == "" && required {
		panic(fmt.Sprintf("Error: required env variable %s not set", key))
	}
	return
}

func main() {
	port := flag.String("port", "8080", "port to listen on")
	profile := flag.Bool("profile", false, "enable profiling")
	lobbies := flag.Int("lobbies", 50, "number of lobbies to run")
	logfileLocation := flag.String("logfile", "", "log file to write to")
	flag.Parse()

	*port = fmt.Sprintf(":%s", *port)

	if *profile {
		go func() {
			fmt.Println("Profiling enabled, listening at http://localhost:6060")

			if err := http.ListenAndServe("localhost:6060", nil); err != nil {
				panic(fmt.Sprintf("Pprof server stopped error: %s", err))
			}
		}()
	}

	var logWriter io.Writer
	if *logfileLocation != "" {
		f, err := os.OpenFile(*logfileLocation, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			panic(fmt.Sprintf("Error: unable to open log file %s: %s", *logfileLocation, err))
		}
		logWriter = f
	} else {
		logWriter = os.Stdout
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	})))

	if err := godotenv.Load(); err != nil {
		slog.Warn("unable to load .env", "error", err)
	}
	slog.Info("loaded .env")

	dbUrl := fmt.Sprintf(
		"postgresql://%s:%s@%s:%s/%s",
		loadEnvVariable("DB_USER", true),
		loadEnvVariable("DB_PASSWORD", true),
		loadEnvVariable("DB_HOST", true),
		loadEnvVariable("DB_PORT", true),
		loadEnvVariable("DB", true),
	)
	dbConn, err := pgxpool.New(context.Background(), dbUrl)
	if err != nil {
		slog.Error("unable to connect to db", "error", err)
		return
	}
	slog.Info("connected to db")

	prod := loadEnvVariable("ENV", false) == "prod"
	domain := loadEnvVariable("DOMAIN", true)

	authConfig = &oauth2.Config{
		ClientID:     loadEnvVariable("GOOGLE_CLIENT_ID", true),
		ClientSecret: loadEnvVariable("GOOGLE_CLIENT_SECRET", true),
		Scopes:       []string{"openid", "profile", "email"},
		Endpoint:     google.Endpoint,
	}
	if prod {
		authConfig.RedirectURL = fmt.Sprintf("https://%s/api/callback", domain)
	} else {
		authConfig.RedirectURL = fmt.Sprintf("http://%s%s/api/callback", domain, *port)
	}
	slog.Info("loaded auth config", "redirect_url", authConfig.RedirectURL)

	clientRedirectUrl := loadEnvVariable("CLIENT_REDIRECT_URL", true)
	states := NewStateStore()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth", authHandler(dbConn, states, clientRedirectUrl))
	mux.HandleFunc("/api/callback", callbackHandler(dbConn, states, clientRedirectUrl, domain, prod))
	mux.HandleFunc("/api/logout", authLogoutHandler(dbConn, clientRedirectUrl, domain, prod))
	mux.HandleFunc("/api/game", gameHandler(prod, dbConn, *lobbies))

	handler := logRequest(mux)

	slog.Info(fmt.Sprintf("listening at http://%s%s", domain, *port))
	if err := http.ListenAndServe(*port, handler); err != nil {
		panic(fmt.Sprintf("Error: %s", err))
	}
}
