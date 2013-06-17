bibtex_opened = null;

function bibtex_click(name) {
  if (bibtex_opened !== null) {
    document.getElementById('bibtex_' + bibtex_opened).style['display'] = 'none';
    if (name == bibtex_opened) {
      bibtex_opened = null;
      return;
    }
  }

  bibtex_opened = name;
  document.getElementById('bibtex_' + bibtex_opened).style['display'] = 'inline-block';
}
