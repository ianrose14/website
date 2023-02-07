package strava

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/ianrose14/website/internal"
	"github.com/ianrose14/website/internal/storage"
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

type ApiParams struct {
	ClientId     string
	ClientSecret string
	Hostname     string
}

type KVDB interface {
	Read(ctx context.Context, username string) (*storage.FetchStravaTokensRow, error)
	Write(ctx context.Context, tokens *storage.InsertStravaTokensParams) error
}

type FileDatabase struct {
	filepath string
}

func (db *FileDatabase) Read(_ context.Context, key string) (*storage.FetchStravaTokensRow, error) {
	if !internal.FileExists(db.filepath) {
		return nil, nil
	}

	contents, err := ioutil.ReadFile(db.filepath)
	if err != nil {
		return nil, err
	}

	m := make(map[string]*storage.FetchStravaTokensRow)
	if err := json.Unmarshal(contents, &m); err != nil {
		return nil, err
	}

	return m[key], nil
}

func (db *FileDatabase) Write(_ context.Context, key string, tokens *storage.FetchStravaTokensRow) error {
	m := make(map[string]*storage.FetchStravaTokensRow)

	if internal.FileExists(db.filepath) {
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
	vals map[string]*storage.FetchStravaTokensRow
	mu   sync.Mutex
}

func (db *MemoryDatabase) Read(_ context.Context, username string) (*storage.FetchStravaTokensRow, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.vals[username], nil
}

func (db *MemoryDatabase) Write(_ context.Context, tokens *storage.InsertStravaTokensParams) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.vals[tokens.Username] = &storage.FetchStravaTokensRow{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		CreatedTime:  time.Now(),
		ExpiresAt:    tokens.ExpiresAt,
	}
	return nil
}

type SqliteDb struct {
	query *storage.Queries
}

func NewSqliteDb(db *sql.DB) KVDB {
	return &SqliteDb{query: storage.New(db)}
}

func (db *SqliteDb) Read(ctx context.Context, username string) (*storage.FetchStravaTokensRow, error) {
	row, err := db.query.FetchStravaTokens(ctx, username)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &row, nil
}

func (db *SqliteDb) Write(ctx context.Context, tokens *storage.InsertStravaTokensParams) error {
	return db.query.InsertStravaTokens(ctx, *tokens)
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

func doStravaQuery(ctx context.Context, sessionId string, start, finish time.Time, db KVDB, account *ApiParams) ([]Activity, error) {
	accessToken, err := readAccessToken(ctx, sessionId, db, account)
	if err != nil {
		return nil, fmt.Errorf("failed to read access token: %s", err)
	}

	return getActivities(accessToken, start, finish)
}

func readAccessToken(ctx context.Context, username string, db KVDB, account *ApiParams) (string, error) {
	// read most recent refresh token
	tokens, err := db.Read(ctx, username)
	if err != nil {
		return "", fmt.Errorf("failed to read from database: %s", err)
	}
	if tokens == nil {
		return "", ErrNeedsAuth
	}

	vals := make(url.Values)
	vals.Set("client_id", account.ClientId)
	vals.Set("client_secret", account.ClientSecret)
	vals.Set("grant_type", "refresh_token")
	vals.Set("refresh_token", tokens.RefreshToken)

	rsp, err := http.DefaultClient.PostForm("https://www.strava.com/api/v3/oauth/token", vals)
	if err != nil {
		return "", fmt.Errorf("failed to post: %s", err)
	}
	if err := internal.CheckResponse(rsp); err != nil {
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
	internal.DrainAndClose(rsp.Body)

	if update.TokenType != "Bearer" {
		return "", fmt.Errorf("unexpected returned TokenType: %q", update.TokenType)
	}

	arg := storage.InsertStravaTokensParams{
		Username:     username,
		AccessToken:  update.AccessToken,
		RefreshToken: update.RefreshToken,
		CreatedTime:  time.Now(),
		ExpiresAt:    time.Unix(update.ExpiresAt, 0),
	}

	if err := db.Write(ctx, &arg); err != nil {
		return "", fmt.Errorf("failed to write tokens to db: %s", err)
	}

	return update.AccessToken, nil
}

func exchangeToken(code string, account *ApiParams) (*AuthResponse, error) {
	vals := make(url.Values)
	vals.Set("client_id", account.ClientId)
	vals.Set("client_secret", account.ClientSecret)
	vals.Set("code", code)
	vals.Set("grant_type", "authorization_code")

	rsp, err := http.DefaultClient.PostForm("https://www.strava.com/oauth/token", vals)
	if err != nil {
		return nil, fmt.Errorf("failed to post: %s", err)
	}
	if err := internal.CheckResponse(rsp); err != nil {
		return nil, fmt.Errorf("failed to post: %s", err)
	}

	var authResp AuthResponse
	if err := json.NewDecoder(rsp.Body).Decode(&authResp); err != nil {
		return nil, err
	}

	return &authResp, nil
}

func formatSeconds(s float64) string {
	minutes := int(s / 60.)
	return fmt.Sprintf("%d:%02.0f", minutes, s-float64(60*minutes))
}

func getAuthUrl(account *ApiParams) string {
	return fmt.Sprintf("https://www.strava.com/oauth/authorize?client_id=" + account.ClientId + "&response_type=code&redirect_uri=https://" + account.Hostname + "/strava/exchange_token/&approval_prompt=force&scope=activity:read_all")
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
	defer internal.DrainAndClose(rsp.Body)

	if err := internal.CheckResponse(rsp); err != nil {
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
	defer internal.DrainAndClose(rsp.Body)

	if err := internal.CheckResponse(rsp); err != nil {
		return nil, fmt.Errorf("failed to get: %s", err)
	}

	var profile ProfileInfo
	if err := json.NewDecoder(rsp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("failed to parse body: %s", err)
	}

	return &profile, nil
}
