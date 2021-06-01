package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	StravaClientId = "59096"

	defaultGoalMiles = 250
)

var (
	ErrNeedsAuth = errors.New("needs auth")
)

type KVDB interface {
	Read(key string) (string, error)
	Write(key, value string) error
}

type FileDatabase struct {
	filepath string
}

func (db *FileDatabase) Read(key string) (string, error) {
	if !FileExists(db.filepath) {
		return "", nil
	}

	contents, err := ioutil.ReadFile(db.filepath)
	if err != nil {
		return "", err
	}

	m := make(map[string]string)
	if err := json.Unmarshal(contents, &m); err != nil {
		return "", err
	}
	return m[key], nil
}

func (db *FileDatabase) Write(key, value string) error {
	m := make(map[string]string)

	if FileExists(db.filepath) {
		contents, err := ioutil.ReadFile(db.filepath)
		if err != nil {
			return err
		}

		if err := json.Unmarshal(contents, &m); err != nil {
			return err
		}
	}

	m[key] = value
	contents, err := json.Marshal(m)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(db.filepath, contents, 0644)
}

type MemoryDatabase struct {
	vals map[string]string
	mu   sync.Mutex
}

func (db *MemoryDatabase) Read(key string) (string, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.vals[key], nil
}

func (db *MemoryDatabase) Write(key, value string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.vals[key] = value
	return nil
}

type Activity struct {
	Name           string  `json:"name"`
	DistanceMeters float64 `json:"distance"`
	MovingTime     float64 `json:"moving_time"`
	Type           string  `json:"type"`
	StartDate      string  `json:"start_date"`
}

func (a *Activity) Miles() float64 {
	return 0.621371 * a.DistanceMeters / 1000.
}

type ProfileInfo struct {
	Username      string `json:"username"`
	ProfileMedium string `json:"profile_medium"`
}

type AuthResponse struct {
	TokenType    string `json:"token_type"`
	ExpiresAt    int    `json:"expires_at"`
	RefreshToken string `json:"refresh_token"`
	AccessToken  string `json:"access_token"`
}

func stravaCliMain() {
	doAuth := flag.Bool("auth", false, "do auth flow instead of normal stuff")
	fDayOfYear := flag.Int("doy", time.Now().YearDay(), "which day-of-year")
	fGoalMiles := flag.Float64("miles", defaultGoalMiles, "goal in miles")
	flag.Parse()

	if *doAuth {
		if err := doAuthFlow(); err != nil {
			log.Fatalf("failed auth flow: %s", err)
		}
	}

	scaledGoalMiles := *fGoalMiles * float64(*fDayOfYear) / 365
	db := &FileDatabase{filepath: "strava.db"}

	activities, err := doStravaQuery(scaledGoalMiles, *fDayOfYear, db)
	if err != nil {
		log.Fatalf("failed: %s", err)
	}

	var count int
	var sumMiles float64
	for _, activity := range activities {
		if activity.Type != "Run" {
			continue
		}
		count++
		sumMiles += activity.Miles()
	}

	log.Printf("found %d running activities in this time range, totalling %.1f miles, %.0f%% of goal",
		count, sumMiles, 100*sumMiles/scaledGoalMiles)

	for _, activity := range activities {
		if activity.Type != "Run" {
			continue
		}

		fmt.Printf("%s: %.1fK (%.1f miles) in %s on %s\n", activity.Name, activity.DistanceMeters/1000.,
			activity.Miles(), formatSeconds(activity.MovingTime), activity.StartDate)
	}
}

func doStravaQuery(scaledGoalMiles float64, dayOfYear int, db KVDB) ([]Activity, error) {
	accessToken, err := readAccessToken(db)
	if err != nil {
		return nil, fmt.Errorf("failed to read access token: %s", err)
	}

	start := time.Date(time.Now().Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	finish := start.AddDate(0, 0, dayOfYear) // finish is intentionally midnight at the END of the day

	log.Printf("Range %s -> %s (scaled goal: %.1f miles)", start, finish, scaledGoalMiles)

	return getActivities(accessToken, start, finish)
}

func readAccessToken(db KVDB) (string, error) {
	// read most recent refresh token
	refreshToken, err := db.Read("refresh_token")
	if err != nil {
		return "", fmt.Errorf("failed to read from database: %s", err)
	}
	if refreshToken == "" {
		return "", ErrNeedsAuth
	}

	vals := make(url.Values)
	vals.Set("client_id", StravaClientId)
	vals.Set("client_secret", StravaClientSecret)
	vals.Set("grant_type", "refresh_token")
	vals.Set("refresh_token", refreshToken)

	rsp, err := http.DefaultClient.PostForm("https://www.strava.com/api/v3/oauth/token", vals)
	if err != nil {
		return "", fmt.Errorf("failed to post: %s", err)
	}
	if err := CheckResponse(rsp); err != nil {
		return "", fmt.Errorf("failed to post: %s", err)
	}

	var update struct {
		TokenType    string `json:"token_type"`
		AccessToken  string `json:"access_token"`
		ExpiresAt    int64  `json:"expires_at"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}

	if err := json.NewDecoder(rsp.Body).Decode(&update); err != nil {
		return "", fmt.Errorf("failed to parse response: %s", err)
	}
	DrainAndClose(rsp.Body)

	if update.TokenType != "Bearer" {
		return "", fmt.Errorf("unexpected returned TokenType: %q", update.TokenType)
	}

	if err := db.Write("expires_at", strconv.FormatInt(update.ExpiresAt, 10)); err != nil {
		return "", fmt.Errorf("failed to write expires_at to db: %s", err)
	}
	if err := db.Write("access_token", update.AccessToken); err != nil {
		return "", fmt.Errorf("failed to write access_token to db: %s", err)
	}
	if err := db.Write("refresh_token", update.RefreshToken); err != nil {
		return "", fmt.Errorf("failed to write refresh_token to db: %s", err)
	}

	return update.AccessToken, nil
}

func getActivities(accessToken string, start, finish time.Time) ([]Activity, error) {
	urls := fmt.Sprintf("https://www.strava.com/api/v3/athlete/activities?per_page=200&before=%d&after=%d", finish.Unix(), start.Unix())
	req, err := http.NewRequest("GET", urls, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %s", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	rsp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get: %s", err)
	}
	defer DrainAndClose(rsp.Body)

	if err := CheckResponse(rsp); err != nil {
		return nil, fmt.Errorf("failed to get: %s", err)
	}

	var activities []Activity
	if err := json.NewDecoder(rsp.Body).Decode(&activities); err != nil {
		return nil, fmt.Errorf("failed to parse body: %s", err)
	}

	return activities, nil
}

func getProfile(accessToken string) (*ProfileInfo, error) {
	req, err := http.NewRequest("GET", "https://www.strava.com/api/v3/athlete", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %s", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	rsp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get: %s", err)
	}
	defer DrainAndClose(rsp.Body)

	if err := CheckResponse(rsp); err != nil {
		return nil, fmt.Errorf("failed to get: %s", err)
	}

	var profile ProfileInfo
	if err := json.NewDecoder(rsp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("failed to parse body: %s", err)
	}

	return &profile, nil
}

func formatSeconds(s float64) string {
	minutes := int(s / 60.)
	return fmt.Sprintf("%d:%02.0f", minutes, s-float64(60*minutes))
}

func StravaAuthUrl(hostname string) string {
	return fmt.Sprintf("https://www.strava.com/oauth/authorize?client_id=" + StravaClientId + "&response_type=code&redirect_uri=https://" + hostname + "/strava/exchange_token/&approval_prompt=force&scope=activity:read_all")
}

func doAuthFlow() error {
	fmt.Println("Visit: " + StravaAuthUrl("localhost"))

	fmt.Println("")
	fmt.Printf("Enter code from redirect URL: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return errors.New("cancelled")
	}
	code := strings.TrimSpace(scanner.Text())

	rsp, err := StravaExchangeToken(code)
	if err != nil {
		return err
	}

	fmt.Printf("%+v\n", rsp)
	return nil
}

func StravaExchangeToken(code string) (*AuthResponse, error) {
	vals := make(url.Values)
	vals.Set("client_id", StravaClientId)
	vals.Set("client_secret", StravaClientSecret)
	vals.Set("code", code)
	vals.Set("grant_type", "authorization_code")

	rsp, err := http.DefaultClient.PostForm("https://www.strava.com/oauth/token", vals)
	if err != nil {
		return nil, fmt.Errorf("failed to post: %s", err)
	}
	if err := CheckResponse(rsp); err != nil {
		return nil, fmt.Errorf("failed to post: %s", err)
	}

	var authResp AuthResponse
	if err := json.NewDecoder(rsp.Body).Decode(&authResp); err != nil {
		return nil, err
	}

	return &authResp, nil
}
