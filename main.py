#!/usr/bin/env python

import jinja2
import json
import os
import urllib2
import webapp2


#
# CONSTANTS
#
CFG_URL = 'https://www.dropbox.com/s/kr8ewc68husts57/albums.json?dl=1'

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
      if 'dropbox.com' in album['url']:
        album['icon'] = 'images/dropbox-icon.png'
        album['icon_alt'] = 'Dropbox'
        album['icon_height'] = '40px'
      elif 'plus.google.com' in album['url']:
        album['icon'] = 'images/gplus-icon.svg'
        album['icon_alt'] = 'Google+'
        album['icon_height'] = '32px'
    
    template = JINJA_ENVIRONMENT.get_template('templates/albums.html')
    self.response.write(template.render(cfg))
    
  def show_error(self, s):
    self.response.write('<h3>Error!</h3><div>%s</div>' % s)


#
# Executed by app.yaml
#

JINJA_ENVIRONMENT = jinja2.Environment(
    loader=jinja2.FileSystemLoader(os.path.dirname(__file__)),
    extensions=['jinja2.ext.autoescape'])
    
routes = [('/albums/?', AlbumsHandler)]
app = webapp2.WSGIApplication(routes, debug=True)
