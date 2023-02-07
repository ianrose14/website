package main

import (
	"context"
	"database/sql"
	"embed"
	_ "embed"
	"flag"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/ianrose14/website/internal"
	"github.com/ianrose14/website/internal/storage"
	"github.com/ianrose14/website/internal/strava"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/acme/autocert"
)

var (
	//go:embed static/*
	staticFS embed.FS

	//go:embed pubs/*
	publicationsFS embed.FS

	//go:embed talks/*
	talksFS embed.FS

	//stravaVars = &internal.MemoryDatabase{vals: make(map[string]*internal.StravaTokens)}
	stravaTemplate = template.Must(template.ParseFiles("templates/strava.html"))
)

func init() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Llongfile)
}

func main() {
	certsDir := flag.String("certs", "certs", "directory to store letsencrypt certs")
	dbfile := flag.String("db", "solarsnoop.sqlite", "sqlite database file")
	host := flag.String("host", "", "optional hostname for webserver")
	secretsFile := flag.String("secrets", "config/secrets.yaml", "Path to local secrets file")
	flag.Parse()

	ctx := context.Background()

	secrets, err := internal.ParseSecrets(*secretsFile)
	if err != nil {
		log.Fatalf("failed to parse secrets: %s", err)
	}

	db, err := sql.Open("sqlite3", "file:"+*dbfile+"?cache=shared")
	if err != nil {
		log.Fatalf("failed to open sqlite connection: %s", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("warning: failed to cleanly close database: %s", err)
		}
	}()

	if err := storage.UpsertDatabaseTables(ctx, db); err != nil {
		log.Fatalf("failed to upsert database tables: %s", err)
	}

	svr := &server{
		db:      db,
		secrets: secrets,
		host:    *host,
	}

	stravaAccount := &strava.ApiParams{
		ClientId:     secrets.Strava.ClientID,
		ClientSecret: secrets.Strava.ClientSecret,
	}

	httpFS := func(files embed.FS, subdir string) http.Handler {
		d, err := fs.Sub(files, subdir)
		if err != nil {
			log.Fatalf("static file config error: %+v", err)
		}
		return http.FileServer(http.FS(d))
	}

	mux := http.NewServeMux()
	mux.Handle("/", httpFS(staticFS, "static"))
	mux.Handle("/pubs/", http.FileServer(http.FS(publicationsFS)))
	mux.Handle("/talks/", http.FileServer(http.FS(talksFS)))

	mux.HandleFunc("/albums/", svr.albumsHandler)
	mux.HandleFunc("/albums/thumbnail/", svr.thumbnailHandler)
	mux.HandleFunc("/dump/", svr.dumpHandler)

	stravaDb := strava.NewSqliteDb(db)
	h := func(w http.ResponseWriter, r *http.Request) {
		strava.Handler(w, r, stravaTemplate, stravaDb, stravaAccount)
	}
	mux.HandleFunc("/running/", h)
	mux.HandleFunc("/strava/", h)
	http.HandleFunc("/strava/exchange_token/", func(w http.ResponseWriter, r *http.Request) {
		strava.TokenHandler(w, r, stravaDb, stravaAccount)
	})

	mux.Handle("/favicon.ico", httpFS(staticFS, "static"))

	var httpHandler http.Handler = mux

	// TODO: in a handler wrapper, redirect http to https (in production only)

	const inDev = runtime.GOOS == "darwin"

	// when testing locally it doesn't make sense to start
	// HTTPS server, so only do it in production.
	// In real code, I control this with -production cmd-line flag
	if !inDev {
		if err := os.MkdirAll(*certsDir, 0777); err != nil {
			log.Fatalf("failed to create certs dir: %s", err)
		}

		httpsSrv := makeHTTPServer(mux)
		certManager := &autocert.Manager{
			Prompt: autocert.AcceptTOS,
			Email:  "ianrose14+autocert@gmail.com", // NOSUBMIT - replace with alias?
			HostPolicy: func(ctx context.Context, host string) error {
				log.Printf("autocert query for host %q", host)
				return autocert.HostWhitelist("solarsnoop.com", "www.solarsnoop.com")(ctx, host)
			},
			Cache: autocert.DirCache(*certsDir),
		}
		httpsSrv.Addr = ":https"
		httpsSrv.TLSConfig = certManager.TLSConfig()

		httpHandler = certManager.HTTPHandler(mux)

		go func() {
			log.Printf("listening on port 443")

			err := httpsSrv.ListenAndServeTLS("", "")
			if err != nil {
				log.Fatalf("httpsSrv.ListendAndServeTLS() failed: %s", err)
			}
		}()
	}

	log.Printf("listening on port 80")
	if err := http.ListenAndServe(":http", httpHandler); err != nil {
		log.Fatalf("http.ListenAndServe failed: %s", err)
	}
}

func makeHTTPServer(mux *http.ServeMux) *http.Server {
	// set timeouts so that a slow or malicious client can't hold resources forever
	return &http.Server{
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  120 * time.Second,
		Handler:      mux,
	}
}

type server struct {
	db      *sql.DB
	secrets *internal.SecretsFile
	host    string
}
