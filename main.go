package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"

	"google.golang.org/appengine"
	"google.golang.org/appengine/log"
	"google.golang.org/appengine/urlfetch"
)

const (
	AlbumsConfigUrl   = "https://www.dropbox.com/s/kr8ewc68husts57/albums.json?dl=1"
	KidlinksConfigUrl = "https://www.dropbox.com/s/5vdvc3l1pkly94f/weblinks.json?dl=1"
)

var (
	albumsTemplate   = template.Must(template.ParseFiles("templates/albums.html"))
	kidLinksTemplate = template.Must(template.ParseFiles("templates/kidlinks.html"))
)

type album struct {
	Name      string `json:"name"`
	Url       string `json:"url"`
	CoverPath string `json:"cover_path"`
	CoverUrl  string
}

// Serves a page that lists all available photo albums.
func AlbumsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := appengine.NewContext(r)
	rsp, err := urlfetch.Client(ctx).Get(AlbumsConfigUrl)
	if err != nil {
		HttpError(ctx, w, http.StatusInternalServerError, "failed to fetch albums config from dropbox: %s", err)
		return
	}
	defer DrainAndClose(rsp.Body)

	if err := CheckResponse(rsp); err != nil {
		HttpError(ctx, w, http.StatusInternalServerError, "failed to fetch albums config from dropbox: %s", err)
		return
	}

	var results struct {
		Albums []*album `json:"albums"`
	}

	if err := json.NewDecoder(rsp.Body).Decode(&results); err != nil {
		HttpError(ctx, w, http.StatusInternalServerError, "failed to json-decode response: %s", err)
		return
	}

	for _, album := range results.Albums {
		album.CoverUrl = fmt.Sprintf("/albums/thumbnail?path=%s", url.QueryEscape(album.CoverPath))
	}

	if err := albumsTemplate.Execute(w, results.Albums); err != nil {
		HttpError(ctx, w, http.StatusInternalServerError, "failed to render template: %s", err)
		return
	}
}

func ThumbnailHandler(w http.ResponseWriter, r *http.Request) {
	ctx := appengine.NewContext(r)

	path := r.URL.Query().Get("path")
	if path == "" {
		HttpError(ctx, w, http.StatusBadRequest, "no path query")
		return
	}

	if !strings.HasPrefix(path, "/photos/") {
		HttpError(ctx, w, http.StatusBadRequest, "rejecting forbidden path %s", path)
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
		HttpError(ctx, w, http.StatusInternalServerError, "failed to json-encode params: %s", err)
		return
	}

	qs := make(url.Values)
	qs.Add("arg", string(jstr))

	urls := "https://api-content.dropbox.com/2/files/get_thumbnail?" + qs.Encode()
	log.Debugf(ctx, "fetching %s", urls)

	req, err := http.NewRequest("GET", urls, nil)
	if err != nil {
		HttpError(ctx, w, http.StatusInternalServerError, "failed to make new http request")
		return
	}

	req.Header.Set("Authorization", "Bearer "+DropboxAccessToken)

	rsp, err := urlfetch.Client(ctx).Do(req)
	if err != nil {
		HttpError(ctx, w, http.StatusInternalServerError, "failed to fetch thumbnail from dropbox: %s", err)
		return
	}
	defer DrainAndClose(rsp.Body)

	if err := CheckResponse(rsp); err != nil {
		HttpError(ctx, w, http.StatusInternalServerError, "failed to fetch thumbnail from dropbox: %s", err)
		return
	}

	w.Header().Set("Content-Type", rsp.Header.Get("Content-Type"))
	if _, err := io.Copy(w, rsp.Body); err != nil {
		log.Warningf(ctx, "failed to copy thumbnail body to response stream: %s", err)
	}
}

/*

class KidsHandler(webapp2.RequestHandler):
  def get(self):
    try:
      rsp = urllib2.urlopen(KidlinksConfigUrl)
    except urllib2.HttpError, e:
      self.show_error("Failed to fetch kidlinks (http %d: %s)" % (e.code, e.reason))
      return
    except urllib2.URLError, e:
      self.show_error("Failed to fetch kidlinks (%s)" % str(e))
      return

    cfg = json.load(rsp)

    template = JINJA_ENVIRONMENT.get_template("templates/kidlinks.html")
    self.response.write(template.render(cfg))



#
# Executed by app.yaml
#

JINJA_ENVIRONMENT = jinja2.Environment(
    loader=jinja2.FileSystemLoader(os.path.dirname(__file__)),
    extensions=["jinja2.ext.autoescape"])

routes = [
  RedirectRoute("/albums/", handler=AlbumsHandler, strict_slash=True, name="albums"),
  webapp2.Route("/albums/thumbnail", handler=ThumbnailHandler, name="thumbnail"),
  RedirectRoute("/kids/", handler=KidsHandler, strict_slash=True, name="kids"),
  ]
app = webapp2.WSGIApplication(routes, debug=True)

*/

func main() {
	http.HandleFunc("/albums", AlbumsHandler)
	http.HandleFunc("/albums/thumbnail", ThumbnailHandler)
	appengine.Main()
}
