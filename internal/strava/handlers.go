package strava

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ianrose14/website/internal"
	"github.com/ianrose14/website/internal/storage"
)

func Handler(w http.ResponseWriter, r *http.Request, tmpl *template.Template, db KVDB, account *ApiParams) {
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
		http.Redirect(w, r, getAuthUrl(account), http.StatusTemporaryRedirect)
		return
	}

	accessToken, err := readAccessToken(r.Context(), username, db, account)
	if err != nil {
		if err == ErrNeedsAuth {
			http.Redirect(w, r, getAuthUrl(account), http.StatusTemporaryRedirect)
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

	now := time.Now()
	goalMiles := defaultGoalMiles[year]
	queryStart := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)

	var scaledGoalMiles float64
	var queryEnd time.Time

	if now.Year() == year {
		scaledGoalMiles = float64(goalMiles) * float64(now.YearDay()) / 365
		queryEnd = queryStart.AddDate(0, 0, now.YearDay()) // finish is intentionally midnight at the END of the day
	} else {
		scaledGoalMiles = float64(goalMiles)
		queryEnd = time.Date(year+1, time.January, 1, 0, 0, 0, 0, now.Location()) // Midnight, start of new years day
	}

	activities, err := doStravaQuery(r.Context(), username, queryStart, queryEnd, db, account)
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
		MilesYearGoal:   goalMiles,
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

	if err := tmpl.Execute(w, &args); err != nil {
		internal.HttpError(w, http.StatusInternalServerError, "failed to render template: %s", err)
		return
	}
}

func TokenHandler(w http.ResponseWriter, r *http.Request, db KVDB, account *ApiParams) {
	code := r.URL.Query().Get("code")
	if code == "" {
		return
	}

	rsp, err := exchangeToken(code, account)
	if err != nil {
		http.Error(w, fmt.Sprintf("failure in token exchange: %s", err), http.StatusInternalServerError)
		return
	}

	arg := storage.InsertStravaTokensParams{
		AccessToken:  rsp.AccessToken,
		RefreshToken: rsp.RefreshToken,
		CreatedTime:  time.Now(),
		ExpiresAt:    time.Unix(rsp.ExpiresAt, 0),
	}

	profile, err := getProfile(rsp.AccessToken)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get profile info: %s", err), http.StatusInternalServerError)
		return
	}

	arg.Username = profile.Username

	if err := db.Write(r.Context(), &arg); err != nil {
		http.Error(w, fmt.Sprintf("failure in write tokens to db: %s", err), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/running/?username="+url.QueryEscape(profile.Username)+"&year="+strconv.Itoa(time.Now().Year()), http.StatusTemporaryRedirect)
}
