#!/usr/bin/env python

#
# IMPORTS
#

import sys
sys.path.append('deps')

from google.appengine.api import urlfetch
import httplib
import jinja2
import json
import logging
import os
import urllib
import urllib2
import webapp2
from webapp2_extras.routes import RedirectRoute


#
# CONSTANTS
#
CFG_URL = 'https://www.dropbox.com/s/kr8ewc68husts57/albums.json?dl=1'
ACCESS_TOKEN = 'wThl210Kpx4AAAAAAAA749UieNrexm9F5VJ3eOdrEB0X5I_LmiDjQ2FT7gMolnEl'


#
# GLOBALS
#
logger = logging.getLogger('main')


#
# CLASSES
#
class AlbumsHandler(webapp2.RequestHandler):
  """ Serves a page that lists all available photo albums. """
  def get(self):
    try:
      rsp = urllib2.urlopen(CFG_URL)
    except urllib2.HTTPError, e:
      self.show_error('Failed to fetch albums list (http %d: %s)' % (e.code, e.reason))
      return
    except urllib2.URLError, e:
      self.show_error('Failed to fetch albums list (%s)' % str(e))
      return

    cfg = json.load(rsp)

    for album in cfg['albums']:
      if 'cover_path' in album:
        album['cover_url'] = '%s?path=%s' % (self.uri_for('thumbnail'), urllib.quote(album['cover_path']))

      if 'dropbox.com' in album['url']:
        album['icon'] = '/images/dropbox-icon.png'
        album['icon_alt'] = 'Dropbox'
        album['icon_height'] = '40px'
      elif 'plus.google.com' in album['url']:
        album['icon'] = '/images/gplus-icon.svg'
        album['icon_alt'] = 'Google+'
        album['icon_height'] = '32px'

    template = JINJA_ENVIRONMENT.get_template('templates/albums.html')
    self.response.write(template.render(cfg))

  def show_error(self, s):
    self.response.write('<h3>Error!</h3><div>%s</div>' % s)


class ThumbnailHandler(webapp2.RequestHandler):
  def get(self):
    src_path = self.request.GET.get('path')
    if src_path is None:
      webapp2.abort(httplib.BAD_REQUEST)

    if not src_path.startswith('photos'):
      logger.debug('rejecting forbidden src_path: %s', src_path)
      webapp2.abort(httplib.FORBIDDEN)

    url = os.path.join('https://api-content.dropbox.com/1/thumbnails/auto/', urllib.quote(src_path))
    logger.debug('fetching %s', url)
    rsp = urlfetch.fetch(url+'?size=l', headers={'Authorization': 'Bearer %s' % ACCESS_TOKEN})
    if rsp.status_code == httplib.OK:
      self.response.content_type = rsp.headers['Content-Type']
      self.response.write(rsp.content)
    else:
      webapp2.abort(rsp.status_code)


#
# Executed by app.yaml
#

JINJA_ENVIRONMENT = jinja2.Environment(
    loader=jinja2.FileSystemLoader(os.path.dirname(__file__)),
    extensions=['jinja2.ext.autoescape'])

routes = [
  RedirectRoute('/albums/', handler=AlbumsHandler, strict_slash=True, name='albums'),
  webapp2.Route('/albums/thumbnail', handler=ThumbnailHandler, name='thumbnail'),
  ]
app = webapp2.WSGIApplication(routes, debug=True)
