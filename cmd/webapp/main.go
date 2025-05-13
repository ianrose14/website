package main

import (
	"context"
	"database/sql"
	"embed"
	_ "embed"
	"errors"
	"flag"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

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

	baseHosts = []string{
		"ianthomasrose.com",
		"allisonrosememorialfund.org",
	}
)

func init() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Llongfile)
}

func main() {
	certsDir := flag.String("certs", "certs", "Directory to store letsencrypt certs")
	dbfile := flag.String("db", "store.sqlite", "sqlite database file")
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

	log.Printf("Starting up, with -certs=%s, -db=%s", *certsDir, *dbfile)

	s, err := filepath.Abs(*certsDir)
	if err != nil {
		log.Fatalf("failed to get absolute path of %q: %+v", *certsDir, err)
	}
	certsDir = &s

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

	svr := &server{db: db}

	stravaAccount := &strava.ApiParams{
		ClientId:     stravaClientID,
		ClientSecret: stravaClientSecret,
		Hostname:     baseHosts[0],
	}

	httpFS := func(files embed.FS, subdir string) http.Handler {
		d, err := fs.Sub(files, subdir)
		if err != nil {
			log.Fatalf("static file config error: %+v", err)
		}
		return http.FileServer(http.FS(d))
	}

	fundMux := http.NewServeMux()
	fundMux.HandleFunc("/", svr.scholarshipFundHandler)

	baseMux := http.NewServeMux()
	baseMux.Handle("/", httpFS(staticFS, "static"))
	baseMux.Handle("/pubs/", http.FileServer(http.FS(publicationsFS)))
	baseMux.Handle("/talks/", http.FileServer(http.FS(talksFS)))

	baseMux.HandleFunc("/albums/", svr.albumsHandler)
	baseMux.HandleFunc("/albums/thumbnail/", svr.thumbnailHandler)
	baseMux.HandleFunc("/allison", svr.allisonHandler)
	baseMux.HandleFunc("/dump/", svr.dumpHandler)

	stravaDb := strava.NewSqliteDb(db)
	baseMux.HandleFunc("/strava/exchange_token/", func(w http.ResponseWriter, r *http.Request) {
		strava.TokenHandler(w, r, stravaDb, stravaAccount)
	})
	{
		h := func(w http.ResponseWriter, r *http.Request) {
			strava.Handler(w, r, stravaTemplate, stravaDb, stravaAccount)
		}
		baseMux.HandleFunc("/running/", h)
		baseMux.HandleFunc("/strava/", h)
	}

	topMux := http.NewServeMux()
	topMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.Host, "allisonrosememorialfund.org") {
			fundMux.ServeHTTP(w, r)
		} else {
			baseMux.ServeHTTP(w, r)
		}
	})

	topMux.Handle("/favicon.ico", httpFS(staticFS, "static"))

	var httpHandler http.Handler

	// TODO: in a handler wrapper, redirect http to https (in production only)

	if !inDev {
		log.Printf("starting autocert manager with certsDir=%v", *certsDir)
		if err := os.MkdirAll(*certsDir, 0777); err != nil {
			log.Fatalf("failed to create certs dir: %s", err)
		}

		var allHosts []string
		for _, h := range baseHosts {
			allHosts = append(allHosts, h, "www."+h)
		}

		httpsSrv := makeHTTPServer(topMux)
		certManager := &autocert.Manager{
			Prompt: autocert.AcceptTOS,
			Email:  "ianrose14+autocert@gmail.com",
			HostPolicy: func(ctx context.Context, host string) error {
				log.Printf("autocert query for host %q, responding with %v", host, allHosts)
				return autocert.HostWhitelist(allHosts...)(ctx, host)
			},
			Cache: autocert.DirCache(*certsDir),
		}
		httpsSrv.Addr = ":https"
		httpsSrv.TLSConfig = certManager.TLSConfig()
		go func() {
			<-ctx.Done()
			if err := httpsSrv.Close(); err != nil {
				log.Printf("error: failed to close https server: %+v", err)
			}
		}()

		httpHandler = certManager.HTTPHandler(http.HandlerFunc(redirectToHttps))

		go func() {
			log.Printf("listening on %s", httpsSrv.Addr)
			if err := httpsSrv.ListenAndServeTLS("", ""); err != nil {
				if !errors.Is(err, http.ErrServerClosed) {
					log.Fatalf("failure in https server: %+v", err)
				}
			}
		}()
	}

	srv := &http.Server{Handler: httpHandler}
	srv.Addr = ":http"
	go func() {
		<-ctx.Done()
		if err := srv.Close(); err != nil {
			log.Printf("error: failed to close http server: %+v", err)
		}
	}()

	log.Printf("listening on %s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil {
		if !errors.Is(err, http.ErrServerClosed) {
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
	db   *sql.DB
	host string
}
