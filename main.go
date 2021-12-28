package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/datastore"
)

const (
	AlbumsConfigUrl   = "https://www.dropbox.com/s/kr8ewc68husts57/albums.json?dl=1"
	KidLinksConfigUrl = "https://www.dropbox.com/s/5vdvc3l1pkly94f/weblinks.json?dl=1"
)

var (
	albumsTemplate   = template.Must(template.ParseFiles("templates/albums.html"))
	kidLinksTemplate = template.Must(template.ParseFiles("templates/kidlinks.html"))
	stravaTemplate   = template.Must(template.ParseFiles("templates/strava.html"))

	stravaVars = &MemoryDatabase{vals: make(map[string]*StravaTokens)}
)

type album struct {
	Name      string `json:"name"`
	Url       string `json:"url"`
	CoverPath string `json:"cover_path"`
	CoverUrl  string
}

// Serves a page that lists all available photo albums.
func AlbumsHandler(w http.ResponseWriter, _ *http.Request) {
	rsp, err := http.Get(AlbumsConfigUrl)
	if err != nil {
		HttpError(w, http.StatusInternalServerError, "failed to fetch albums config from dropbox: %s", err)
		return
	}
	defer DrainAndClose(rsp.Body)

	if err := CheckResponse(rsp); err != nil {
		HttpError(w, http.StatusInternalServerError, "failed to fetch albums config from dropbox: %s", err)
		return
	}

	var results struct {
		Albums []*album `json:"albums"`
	}

	if err := json.NewDecoder(rsp.Body).Decode(&results); err != nil {
		HttpError(w, http.StatusInternalServerError, "failed to json-decode response: %s", err)
		return
	}

	for _, album := range results.Albums {
		album.CoverUrl = fmt.Sprintf("/albums/thumbnail?path=%s", url.QueryEscape(album.CoverPath))
	}

	if err := albumsTemplate.Execute(w, results.Albums); err != nil {
		HttpError(w, http.StatusInternalServerError, "failed to render template: %s", err)
		return
	}
}

func DumpHandler(w http.ResponseWriter, r *http.Request) {
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

	b, err := ioutil.ReadAll(r.Body)
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

type links struct {
	Sections []struct {
		Title string `json:"title"`
		Links []struct {
			Href  string `json:"href"`
			Text  string `json:"text"`
			Notes string `json:"notes"`
		} `json:"links"`
	} `json:"sections"`
}

func KidsLinksHandler(w http.ResponseWriter, _ *http.Request) {
	rsp, err := http.Get(KidLinksConfigUrl)
	if err != nil {
		HttpError(w, http.StatusInternalServerError, "failed to fetch kid links config from dropbox: %s", err)
		return
	}
	defer DrainAndClose(rsp.Body)

	if err := CheckResponse(rsp); err != nil {
		HttpError(w, http.StatusInternalServerError, "failed to fetch kid links config from dropbox: %s", err)
		return
	}

	var results links
	if err := json.NewDecoder(rsp.Body).Decode(&results); err != nil {
		HttpError(w, http.StatusInternalServerError, "failed to json-decode response: %s", err)
		return
	}

	if err := kidLinksTemplate.Execute(w, &results); err != nil {
		HttpError(w, http.StatusInternalServerError, "failed to render template: %s", err)
		return
	}
}

func StravaHandler(w http.ResponseWriter, r *http.Request, db KVDB) {
	year := time.Now().Year()
	if s := r.URL.Query().Get("year"); s != "" {
		if i, err := strconv.Atoi(s); err == nil {
			year = i
		}
	}

	username := r.URL.Query().Get("username")
	if username == "" {
		c, err := r.Cookie("username")
		if err == nil {
			username = c.Value
		}
	}

	if username == "" {
		http.Redirect(w, r, StravaAuthUrl("www.ianthomasrose.com"), http.StatusTemporaryRedirect)
		return
	}

	accessToken, err := readAccessToken(r.Context(), username, db)
	if err != nil {
		if err == ErrNeedsAuth {
			http.Redirect(w, r, StravaAuthUrl("www.ianthomasrose.com"), http.StatusTemporaryRedirect)
			return
		}

		http.Error(w, fmt.Sprintf("failed to read access token: %s", err), http.StatusInternalServerError)
		return
	}

	profile, err := getProfile(accessToken)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get profile info: %s", err), http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:    "username",
		Value:   username,
		Expires: time.Now().Add(7 * 24 * time.Hour),
	})

	dayOfYear := time.Now().YearDay()
	scaledGoalMiles := float64(defaultGoalMiles[year]) * float64(dayOfYear) / 365

	activities, err := doStravaQuery(r.Context(), username, scaledGoalMiles, dayOfYear, year, db)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to query strava: %s", err), http.StatusInternalServerError)
		return
	}

	args := struct {
		Username        string
		Activities      []string
		MilesTotal      string
		MilesYearGoal   int
		MilesScaledGoal string
		Progress        string
		GaugeRotate     int
	}{
		Username:        profile.Username,
		MilesYearGoal:   defaultGoalMiles[year],
		MilesScaledGoal: fmt.Sprintf("%.1f", scaledGoalMiles),
	}

	var sumMiles float64
	for _, activity := range activities {
		if activity.Type != "Run" {
			continue
		}
		secondsPerMile := int64(activity.MovingTime/activity.Miles() + 0.5000001)
		args.Activities = append(args.Activities,
			fmt.Sprintf("%s: %.1fK (%.1f miles) in %s (%d:%02d pace) on %s", activity.Name,
				activity.DistanceMeters/1000., activity.Miles(),
				formatSeconds(activity.MovingTime), secondsPerMile/60, secondsPerMile%60,
				activity.StartDate))

		sumMiles += activity.Miles()
	}

	progress := 100 * sumMiles / scaledGoalMiles

	args.MilesTotal = fmt.Sprintf("%.1f", sumMiles)
	args.Progress = fmt.Sprintf("%.0f", progress)
	args.GaugeRotate = int(90.0 * progress / 100)

	if err := stravaTemplate.Execute(w, &args); err != nil {
		HttpError(w, http.StatusInternalServerError, "failed to render template: %s", err)
		return
	}
}

func StravaTokenHandler(w http.ResponseWriter, r *http.Request, db KVDB) {
	code := r.URL.Query().Get("code")
	if code == "" {
		return
	}

	rsp, err := StravaExchangeToken(code)
	if err != nil {
		http.Error(w, fmt.Sprintf("failure in token exchange: %s", err), http.StatusInternalServerError)
		return
	}

	tokens := StravaTokens{
		AccessToken:  rsp.AccessToken,
		ExpiresAt:    time.Unix(rsp.ExpiresAt, 0),
		RefreshToken: rsp.RefreshToken,
	}

	profile, err := getProfile(rsp.AccessToken)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get profile info: %s", err), http.StatusInternalServerError)
		return
	}

	if err := db.Write(r.Context(), profile.Username, &tokens); err != nil {
		http.Error(w, fmt.Sprintf("failure in write tokens to db: %s", err), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/running/?username="+url.QueryEscape(profile.Username)+"&year="+strconv.Itoa(time.Now().Year()), http.StatusTemporaryRedirect)
}

func ThumbnailHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		HttpError(w, http.StatusBadRequest, "no path query")
		return
	}

	if !strings.HasPrefix(path, "/photos/") {
		HttpError(w, http.StatusBadRequest, "rejecting forbidden path %s", path)
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
		HttpError(w, http.StatusInternalServerError, "failed to json-encode params: %s", err)
		return
	}

	qs := make(url.Values)
	qs.Add("arg", string(jstr))

	urls := "https://api-content.dropbox.com/2/files/get_thumbnail?" + qs.Encode()
	log.Printf("fetching %s", urls)

	req, err := http.NewRequest("GET", urls, nil)
	if err != nil {
		HttpError(w, http.StatusInternalServerError, "failed to make new http request")
		return
	}

	req.Header.Set("Authorization", "Bearer "+DropboxAccessToken)

	rsp, err := http.DefaultClient.Do(req)
	if err != nil {
		HttpError(w, http.StatusInternalServerError, "failed to fetch thumbnail from dropbox: %s", err)
		return
	}
	defer DrainAndClose(rsp.Body)

	if err := CheckResponse(rsp); err != nil {
		HttpError(w, http.StatusInternalServerError, "failed to fetch thumbnail from dropbox: %s", err)
		return
	}

	w.Header().Set("Content-Type", rsp.Header.Get("Content-Type"))
	if _, err := io.Copy(w, rsp.Body); err != nil {
		log.Printf("failed to copy thumbnail body to response stream: %s", err)
	}
}

func main() {
	if os.Getenv("STRAVA_CLI") != "" {
		stravaCliMain()
		return
	}

	client, err := datastore.NewClient(context.Background(), os.Getenv("GOOGLE_CLOUD_PROJECT"))
	if err != nil {
		log.Fatalf("failed to connect to datastore: %s", err)
	}

	ddb := &DatastoreDb{
		client: client,
	}

	http.HandleFunc("/albums/", AlbumsHandler)
	http.HandleFunc("/albums/thumbnail", ThumbnailHandler)
	http.HandleFunc("/dump", DumpHandler)
	http.HandleFunc("/kids/", KidsLinksHandler)
	http.HandleFunc("/running/", func(w http.ResponseWriter, r *http.Request) {
		StravaHandler(w, r, ddb)
	})
	http.HandleFunc("/strava/exchange_token/", func(w http.ResponseWriter, r *http.Request) {
		StravaTokenHandler(w, r, ddb)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Printf("Defaulting to port %s", port)
	}

	log.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
