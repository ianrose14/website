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
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/ianrose14/website/internal"
	"github.com/ianrose14/website/internal/storage"
	"github.com/ianrose14/website/internal/strava"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/acme/autocert"
)

const inDev = runtime.GOOS == "darwin"

var (
	//go:embed static/*
	staticFS embed.FS

	//go:embed pubs/*
	publicationsFS embed.FS

	//go:embed talks/*
	talksFS embed.FS

	//go:embed templates/*
	templatesFS embed.FS

	//stravaVars = &internal.MemoryDatabase{vals: make(map[string]*internal.StravaTokens)}
	stravaTemplate = template.Must(template.ParseFS(templatesFS, "templates/strava.html"))
)

func init() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Llongfile)
}

func main() {
	certsDir := flag.String("certs", "certs", "Directory to store letsencrypt certs")
	dbfile := flag.String("db", "store.sqlite", "sqlite database file")
	host := flag.String("host", "", "Optional hostname for webserver")
	secretsFile := flag.String("secrets", "config/secrets.yaml", "Path to local secrets file")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-c
		log.Printf("received %q signal", sig)
		cancel()
	}()

	if *host == "" {
		*host = "localhost"
	}

	s, err := filepath.Abs(*certsDir)
	if err != nil {
		log.Fatalf("failed to get absolute path of %q: %+v", *certsDir, err)
	}
	certsDir = &s

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
		Hostname:     *host,
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
	mux.HandleFunc("/strava/exchange_token/", func(w http.ResponseWriter, r *http.Request) {
		strava.TokenHandler(w, r, stravaDb, stravaAccount)
	})
	{
		h := func(w http.ResponseWriter, r *http.Request) {
			strava.Handler(w, r, stravaTemplate, stravaDb, stravaAccount)
		}
		mux.HandleFunc("/running/", h)
		mux.HandleFunc("/strava/", h)
	}

	mux.Handle("/favicon.ico", httpFS(staticFS, "static"))

	var httpHandler http.Handler = mux

	// TODO: in a handler wrapper, redirect http to https (in production only)

	if !inDev {
		log.Printf("starting autocert manager with certsDir=%v", *certsDir)
		if err := os.MkdirAll(*certsDir, 0777); err != nil {
			log.Fatalf("failed to create certs dir: %s", err)
		}

		httpsSrv := makeHTTPServer(mux)
		certManager := &autocert.Manager{
			Prompt: autocert.AcceptTOS,
			Email:  "ianrose14+autocert@gmail.com",
			HostPolicy: func(ctx context.Context, host string) error {
				log.Printf("autocert query for host %q, responding with %v", host, []string{svr.host, "www." + svr.host})
				return autocert.HostWhitelist(svr.host, "www."+svr.host)(ctx, host)
			},
			Cache: autocert.DirCache(*certsDir),
		}
		httpsSrv.Addr = ":https"
		httpsSrv.TLSConfig = certManager.TLSConfig()

		httpHandler = certManager.HTTPHandler(mux)

		lis, err := net.Listen("tcp", ":https")
		if err != nil {
			log.Fatalf("failed to listen on port 443: %+v", err)
		}

		log.Printf("listening on %s", lis.Addr())
		srv := &http.Server{Handler: httpHandler}

		go func() {
			<-ctx.Done()
			if err := srv.Close(); err != nil {
				log.Printf("error: failed to close https server: %+v", err)
			}
		}()

		go func() {
			if err := httpsSrv.ServeTLS(lis, "", ""); err != nil {
				if err != http.ErrServerClosed {
					log.Fatalf("failure in https server: %+v", err)
				}
			}
		}()
	}

	lis, err := net.Listen("tcp", ":http")
	if err != nil {
		log.Fatalf("failed to listen on port 80: %+v", err)
	}

	log.Printf("listening on %s", lis.Addr())
	srv := &http.Server{Handler: httpHandler}
	go func() {
		<-ctx.Done()
		if err := srv.Close(); err != nil {
			log.Printf("error: failed to close http server: %+v", err)
		}
	}()

	if err := srv.Serve(lis); err != nil {
		if err != http.ErrServerClosed {
			log.Fatalf("failure in http server: %+v", err)
		}
	}

	log.Printf("clean exit - goodbye!")
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
