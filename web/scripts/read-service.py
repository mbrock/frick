"""Read-only hypermedia client. One vault read; credentials remain in process memory."""
import html.parser
import json
import os
import pathlib
import subprocess
import sys
import urllib.parse

base = 'https://less.rest'
beta = pathlib.Path.home() / '.local/share/frick-tools/op-beta/op'
stable = pathlib.Path('/usr/local/bin/op')
op = os.environ.get('FRICK_OP', str(stable) if stable.exists() else (str(beta) if beta.exists() else 'op'))
result = subprocess.run([op, 'read', 'op://Personal/Frick Worker Service/password'], capture_output=True, text=True)
if result.returncode:
    raise SystemExit('Credential retrieval failed; output withheld.')
token = result.stdout.strip()

class Document(html.parser.HTMLParser):
    def __init__(self):
        super().__init__(); self.links = []; self.text = []
    def handle_starttag(self, tag, attrs):
        data = dict(attrs)
        if tag == 'a' and 'href' in data: self.links.append(data['href'])
    def handle_data(self, data):
        self.text.append(data)

def read(path):
    target = urllib.parse.urljoin(base, path)
    if urllib.parse.urlsplit(target).netloc != urllib.parse.urlsplit(base).netloc:
        raise ValueError('Only the service origin is allowed.')
    config = 'url = '+json.dumps(target)+'\nsilent\nshow-error\nmax-time = 60\nheader = '+json.dumps('Authorization: Bearer '+token)+'\n'
    result = subprocess.run(['curl', '--config', '-', '--write-out', '\n%{http_code}'], input=config, capture_output=True, text=True)
    if result.returncode: raise RuntimeError('Network request failed; output withheld.')
    body, status = result.stdout.rsplit('\n', 1)
    page = Document(); page.feed(body)
    print(json.dumps({'path': path, 'status': status, 'links': page.links, 'error':next((t for t in page.text if 'Bank redirected' in t or 'Bank request failed' in t or 'Banking relay unavailable' in t), None), 'order_links':sum(x.startswith('/frick/orders/') for x in page.links)}), flush=True)
    return page

if '--interactive' in sys.argv:
    print('Ready for read-only paths; credential is held in memory until exit.', flush=True)
    for line in sys.stdin:
        path = line.strip()
        if path == 'quit': break
        if path: read(path)
elif '--check' in sys.argv:
    page=read('/frick/')
    for link in page.links:
        if link.startswith('/frick/history?'): read(link)
    read('/frick/pending'); read('/frick/payments/new')
else:
    read(sys.argv[1] if len(sys.argv)>1 else '/frick/')
