package main

import (
	"context"
	"database/sql"
	"embed"
	_ "embed"
	"flag"
	"html/template"
	"io/fs"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	daemonize := flag.Bool("d", false, "Whether to daemonize on start")
	dbfile := flag.String("db", "store.sqlite", "sqlite database file")
	host := flag.String("host", "", "optional hostname for webserver")
	pidfile := flag.String("pidfile", "", "Optional file to write process ID to")
	secretsFile := flag.String("secrets", "config/secrets.yaml", "Path to local secrets file")
	flag.Parse()

	if *host == "" {
		*host = "localhost"
	}

	ctx := context.Background()

	s, err := filepath.Abs(*certsDir)
	if err != nil {
		log.Fatalf("failed to get absolute path of %q: %+v", *certsDir, err)
	}
	certsDir = &s

	if *pidfile != "" {
		s, err := filepath.Abs(*pidfile)
		if err != nil {
			log.Fatalf("failed to get absolute path of %q: %+v", *pidfile, err)
		}
		pidfile = &s
	}

	secrets, err := internal.ParseSecrets(*secretsFile)
	if err != nil {
		log.Fatalf("failed to parse secrets: %s", err)
	}

	log.Printf("hello, world!  v1")

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

	if *daemonize {

	}

	if *pidfile != "" {
		if err := ioutil.WriteFile(*pidfile, []byte(strconv.Itoa(os.Getpid())), 0666); err != nil {
			log.Fatalf("failed to write pidfile: %+v", err)
		}
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

	if !inDev {
		if err := os.MkdirAll(*certsDir, 0777); err != nil {
			log.Fatalf("failed to create certs dir: %s", err)
		}

		httpsSrv := makeHTTPServer(mux)
		certManager := &autocert.Manager{
			Prompt: autocert.AcceptTOS,
			Email:  "ianrose14+autocert@gmail.com",
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
