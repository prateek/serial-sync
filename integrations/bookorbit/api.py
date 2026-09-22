import hashlib
import http.cookiejar
import json
import time
import urllib.error
import urllib.request


class APIError(RuntimeError):
    def __init__(self, method, path, status):
        super().__init__(f'BookOrbit {method} {path.split("?")[0]} returned HTTP {status}')
        self.status = status


class BookOrbit:
    def __init__(self, config):
        self.config = config
        self.base = config['url'].rstrip('/') + '/api/v1/'
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.authenticated = False

    def request(self, method, path, data=None, digest=False, authenticated=True):
        if authenticated and not self.authenticated:
            self.login()
        payload = None if data is None else json.dumps(data).encode()
        request = urllib.request.Request(self.base + path, payload, method=method,
                                        headers={'Content-Type': 'application/json', 'Cache-Control': 'no-cache'})
        for attempt in range(2):
            try:
                with self.opener.open(request, timeout=30) as response:
                    if digest:
                        return hashlib.file_digest(response, 'sha256').hexdigest()
                    body = response.read()
                    return json.loads(body) if body else None
            except urllib.error.HTTPError as error:
                if error.code == 401 and authenticated and attempt == 0:
                    self.login()
                    continue
                raise APIError(method, path, error.code) from None
        raise RuntimeError('BookOrbit authentication failed')

    def login(self):
        with open(self.config['credentials_file']) as file:
            credentials = json.load(file)
        self.request('POST', 'auth/login', {key: credentials[key] for key in ('username', 'password')}, authenticated=False)
        self.authenticated = True

    def scan(self):
        library = self.config['library_id']
        job = self.request('POST', f'scanner/libraries/{library}/scan')['jobId']
        deadline = time.monotonic() + self.config.get('readiness_timeout', 300)
        while time.monotonic() < deadline:
            history = self.request('GET', f'scanner/libraries/{library}/scan-history')
            match = next((entry for entry in history if entry['id'] == job), None)
            if match and match['status'] == 'completed':
                if match.get('errorMessage'):
                    raise RuntimeError(f'BookOrbit scan {job} completed with errors')
                return
            if match and match['status'] in ('failed', 'cancelled'):
                raise RuntimeError(f'BookOrbit scan {job} {match["status"]}')
            time.sleep(1)
        raise RuntimeError(f'BookOrbit scan {job} did not finish before the readiness timeout')

    def catalog(self, cached):
        result = {}
        page = 0
        while True:
            response = self.request('POST', f'libraries/{self.config["library_id"]}/books',
                                    {'pagination': {'page': page, 'size': 200}, 'collapseSeries': False})
            for summary in response['items']:
                key = str(summary['id'])
                previous = cached.get(key)
                if previous and previous['updated_at'] == summary['updatedAt']:
                    result[key] = previous
                else:
                    book = self.request('GET', f'books/{key}')
                    result[key] = {'updated_at': summary['updatedAt'], 'status': book['status'],
                                   'files': book['files'], 'title': book['title']}
            if (page + 1) * 200 >= response['total']:
                return result
            page += 1
            if page > 500:
                raise RuntimeError('BookOrbit library exceeds the supported 100,000-book scan window')

    def verify(self, catalog, path, expected):
        matches = [(int(book_id), file) for book_id, book in catalog.items() if book['status'] == 'present'
                   for file in book['files'] if file['absolutePath'] == path]
        if len(matches) != 1:
            raise RuntimeError(f'Expected one imported file for {path}; found {len(matches)}')
        book_id, file = matches[0]
        received = self.request('GET', f'books/files/{file["id"]}/download?serial_sync_version={expected}', digest=True)
        if received != expected:
            raise RuntimeError(f'BookOrbit has not made the expected file version readable: {path}')
        return {'book_id': book_id, 'file_id': file['id'],
                'url': self.config['public_url'].rstrip('/') + f'/read/{book_id}/{file["id"]}?format=epub'}
