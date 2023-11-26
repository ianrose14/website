-- name: InsertStravaTokens :exec
INSERT OR REPLACE INTO strava_tokens(username, access_token, refresh_token, created_time, expires_at) VALUES (?,?,?,?,?);

-- name: FetchStravaTokens :one
SELECT access_token, refresh_token, created_time, expires_at
    FROM strava_tokens
    WHERE username=?;
