package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/datastore"
)

const (
	StravaClientId = "59096"
)

var (
	ErrNeedsAuth = errors.New("needs auth")

	defaultGoalMiles = map[int]int{
		2019: 100,
		2020: 100,
		2021: 250,
		2022: 400,
		2023: 425,
	}
)

type StravaTokens struct {
	AccessToken  string
	ExpiresAt    time.Time
	RefreshToken string
}

type KVDB interface {
	Read(ctx context.Context, key string) (*StravaTokens, error)
	Write(ctx context.Context, key string, tokens *StravaTokens) error
}

type FileDatabase struct {
	filepath string
}

func (db *FileDatabase) Read(ctx context.Context, key string) (*StravaTokens, error) {
	if !FileExists(db.filepath) {
		return nil, nil
	}

	contents, err := ioutil.ReadFile(db.filepath)
	if err != nil {
		return nil, err
	}

	m := make(map[string]*StravaTokens)
	if err := json.Unmarshal(contents, &m); err != nil {
		return nil, err
	}

	return m[key], nil
}

func (db *FileDatabase) Write(ctx context.Context, key string, tokens *StravaTokens) error {
	m := make(map[string]*StravaTokens)

	if FileExists(db.filepath) {
		contents, err := ioutil.ReadFile(db.filepath)
		if err != nil {
			return err
		}

		if err := json.Unmarshal(contents, &m); err != nil {
			return err
		}
	}

	m[key] = tokens
	contents, err := json.Marshal(m)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(db.filepath, contents, 0644)
}

type MemoryDatabase struct {
	vals map[string]*StravaTokens
	mu   sync.Mutex
}

func (db *MemoryDatabase) Read(ctx context.Context, key string) (*StravaTokens, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.vals[key], nil
}

func (db *MemoryDatabase) Write(ctx context.Context, key string, tokens *StravaTokens) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.vals[key] = tokens
	return nil
}

type DatastoreDb struct {
	client *datastore.Client
}

func (db *DatastoreDb) Read(ctx context.Context, key string) (*StravaTokens, error) {
	k := datastore.NameKey("StravaTokens", key, nil)
	var tokens StravaTokens
	if err := db.client.Get(ctx, k, &tokens); err != nil {
		if err == datastore.ErrNoSuchEntity {
			return nil, nil
		}
		return nil, err
	}
	return &tokens, nil
}

func (db *DatastoreDb) Write(ctx context.Context, key string, tokens *StravaTokens) error {
	k := datastore.NameKey("StravaTokens", key, nil)
	_, err := db.client.Put(ctx, k, tokens)
	return err
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
	ExpiresAt    int64  `json:"expires_at"`
	RefreshToken string `json:"refresh_token"`
	AccessToken  string `json:"access_token"`
}

func doStravaQuery(ctx context.Context, sessionId string, start, finish time.Time, db KVDB) ([]Activity, error) {
	accessToken, err := readAccessToken(ctx, sessionId, db)
	if err != nil {
		return nil, fmt.Errorf("failed to read access token: %s", err)
	}

	return getActivities(accessToken, start, finish)
}

func readAccessToken(ctx context.Context, username string, db KVDB) (string, error) {
	// read most recent refresh token
	tokens, err := db.Read(ctx, username)
	if err != nil {
		return "", fmt.Errorf("failed to read from database: %s", err)
	}
	if tokens == nil {
		return "", ErrNeedsAuth
	}

	vals := make(url.Values)
	vals.Set("client_id", StravaClientId)
	vals.Set("client_secret", StravaClientSecret)
	vals.Set("grant_type", "refresh_token")
	vals.Set("refresh_token", tokens.RefreshToken)

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

	tokens = &StravaTokens{
		AccessToken:  update.AccessToken,
		ExpiresAt:    time.Unix(update.ExpiresAt, 0),
		RefreshToken: update.RefreshToken,
	}

	if err := db.Write(ctx, username, tokens); err != nil {
		return "", fmt.Errorf("failed to write tokens to db: %s", err)
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

	log.Printf("got %d activities from %s to %s", len(activities), start, finish)
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
