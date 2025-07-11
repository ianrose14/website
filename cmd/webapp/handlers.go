package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/ianrose14/website/internal"
)

const (
	AlbumsConfigUrl  = "https://www.dropbox.com/s/kr8ewc68husts57/albums.json?dl=1"
	AllisonConfigUrl = "https://www.dropbox.com/scl/fi/6yalddhnyuo2sld7y845e/web-config.json?rlkey=q1c1lztc1tsz8vhy8xwvqdtl9&st=khjv2q6e&dl=1"
)

var (
	albumsTemplate  = template.Must(template.ParseFS(templatesFS, "templates/albums.html"))
	allisonTemplate = template.Must(template.ParseFS(templatesFS, "templates/allison.html"))

	dropboxAccessToken = os.Getenv("DROPBOX_TOKEN")
	stravaClientID     = os.Getenv("STRAVA_CLIENT_ID")
	stravaClientSecret = os.Getenv("STRAVA_SECRET")
)

type album struct {
	Name      string `json:"name"`
	Url       string `json:"url"`
	CoverPath string `json:"cover_path"`
	CoverUrl  string
}

type webItem struct {
	Name     string `json:"name"`
	Desc     string `json:"desc"`
	Url      string `json:"url"`
	LinkText string `json:"linktext"`
}

type webSection struct {
	Name  string     `json:"name"`
	Desc  string     `json:"desc"`
	Items []*webItem `json:"items"`
}

// albumsHandler serves a page that lists all available photo albums.
func (svr *server) albumsHandler(w http.ResponseWriter, _ *http.Request) {
	rsp, err := http.Get(AlbumsConfigUrl)
	if err != nil {
		internal.HttpError(w, http.StatusInternalServerError, "failed to fetch albums config from dropbox: %s", err)
		return
	}
	defer internal.DrainAndClose(rsp.Body)

	if err := internal.CheckResponse(rsp); err != nil {
		internal.HttpError(w, http.StatusInternalServerError, "failed to fetch albums config from dropbox: %s", err)
		return
	}

	var results struct {
		Albums []*album `json:"albums"`
	}

	if err := json.NewDecoder(rsp.Body).Decode(&results); err != nil {
		internal.HttpError(w, http.StatusInternalServerError, "failed to json-decode response: %s", err)
		return
	}

	for _, album := range results.Albums {
		album.CoverUrl = "/images/covers/" + album.CoverPath
	}

	if err := albumsTemplate.Execute(w, results.Albums); err != nil {
		internal.HttpError(w, http.StatusInternalServerError, "failed to render template: %s", err)
		return
	}
}

func (svr *server) allisonHandler(w http.ResponseWriter, _ *http.Request) {
	rsp, err := http.Get(AllisonConfigUrl)
	if err != nil {
		internal.HttpError(w, http.StatusInternalServerError, "failed to fetch web config file from dropbox: %s", err)
		return
	}
	defer internal.DrainAndClose(rsp.Body)

	if err := internal.CheckResponse(rsp); err != nil {
		internal.HttpError(w, http.StatusInternalServerError, "failed to fetch web config file from dropbox: %s", err)
		return
	}

	var results struct {
		Sections []*webSection `json:"sections"`
	}

	if err := json.NewDecoder(rsp.Body).Decode(&results); err != nil {
		internal.HttpError(w, http.StatusInternalServerError, "failed to json-decode response: %s", err)
		return
	}

	if err := allisonTemplate.Execute(w, results.Sections); err != nil {
		internal.HttpError(w, http.StatusInternalServerError, "failed to render template: %s", err)
		return
	}
}

func (svr *server) dumpHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")

	writeIt := func(s string, args ...interface{}) {
		log.Printf(s, args...)
		fmt.Fprintf(w, s+"\n", args...)
	}

	writeIt("URL: %s", r.URL)
	writeIt("Method: %s", r.Method)
	writeIt("Proto: %s", r.Proto)
	writeIt("RemoteAddr: %s", r.RemoteAddr)

	var buf bytes.Buffer
	buf.WriteString("Headers:\n")
	for k, v := range r.Header {
		fmt.Fprintf(&buf, "%s: %v\n", k, v)
	}
	writeIt("%s", buf.String())
	fmt.Fprintln(w, "") // write blank line to response body

	buf.Reset()
	buf.WriteString("Cookies:\n")
	for _, c := range r.Cookies() {
		fmt.Fprintf(&buf, "%s\n", c)
	}
	writeIt("%s", buf.String())
	fmt.Fprintln(w, "") // write blank line to response body

	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeIt("error: failed to read body: %s", err)
	} else {
		buf.Reset()
		if _, err := base64.NewEncoder(base64.StdEncoding, &buf).Write(b); err != nil {
			writeIt("error: failed to base64-encode body: %s", err)
		} else {
			writeIt("Body (base64):\n%s\n", buf.String())
		}
		fmt.Fprintln(w, "") // write blank line to response body

		writeIt("Body (raw):\n%s", string(b))
		fmt.Fprintln(w, "") // write blank line to response body
	}
}

func (svr *server) scholarshipFundHandler(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprintf(w, "Coming soon!  Please check back in a few weeks.")
}

func (svr *server) thumbnailHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		internal.HttpError(w, http.StatusBadRequest, "no path query")
		return
	}

	if !strings.HasPrefix(path, "/photos/") {
		internal.HttpError(w, http.StatusBadRequest, "rejecting forbidden path %s", path)
		return
	}

	params := struct {
		Path   string `json:"path"`
		Format string `json:"format"`
		Size   string `json:"size"`
	}{
		Path:   path,
		Format: "jpeg",
		Size:   "w640h480",
	}

	jstr, err := json.Marshal(&params)
	if err != nil {
		internal.HttpError(w, http.StatusInternalServerError, "failed to json-encode params: %s", err)
		return
	}

	qs := make(url.Values)
	qs.Add("arg", string(jstr))

	urls := "https://api-content.dropbox.com/2/files/get_thumbnail?" + qs.Encode()
	log.Printf("fetching %s", urls)

	req, err := http.NewRequest("GET", urls, nil)
	if err != nil {
		internal.HttpError(w, http.StatusInternalServerError, "failed to make new http request")
		return
	}

	req.Header.Set("Authorization", "Bearer "+dropboxAccessToken)

	rsp, err := http.DefaultClient.Do(req)
	if err != nil {
		internal.HttpError(w, http.StatusInternalServerError, "failed to fetch thumbnail from dropbox: %s", err)
		return
	}
	defer internal.DrainAndClose(rsp.Body)

	if err := internal.CheckResponse(rsp); err != nil {
		internal.HttpError(w, http.StatusInternalServerError, "failed to fetch thumbnail from dropbox: %s", err)
		return
	}

	w.Header().Set("Content-Type", rsp.Header.Get("Content-Type"))
	if _, err := io.Copy(w, rsp.Body); err != nil {
		log.Printf("failed to copy thumbnail body to response stream: %s", err)
	}
}

func redirectToHttps(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil {
		host, _, _ := net.SplitHostPort(r.Host)
		u := r.URL
		u.Host = net.JoinHostPort(host, "443")
		u.Scheme = "https"
		http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
	} else {
		http.Error(w, "unsupported protocol: "+r.URL.String(), http.StatusBadRequest)
	}
}
