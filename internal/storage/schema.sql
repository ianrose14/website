CREATE TABLE IF NOT EXISTS strava_tokens (
    username TEXT NOT NULL PRIMARY KEY,
    access_token TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    created_time DATE NOT NULL,
    expires_at DATE NOT NULL
);
