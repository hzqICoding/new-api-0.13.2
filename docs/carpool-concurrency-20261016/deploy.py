"""r15.14: carpool per-user concurrency limit (backend only; frontend embedded unchanged from r15.13).

Run on serverB:
  python3 deploy.py prepare   # baseline checks, embedded-asset verification, image build; production unchanged
  python3 deploy.py stage     # isolated SQLite instance + mock upstream, real relay requests
  python3 deploy.py cutover   # switch production container (auto-rollback on failure)
  python3 deploy.py verify    # live checks
  python3 deploy.py rollback  # restore r15.13 container
"""
import hashlib
import http.cookiejar
import importlib.util
import json
import pathlib
import secrets
import sys
import threading
import time
import urllib.error
import urllib.request
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = pathlib.Path('/opt/carpool/releases/newapi-concurrency-20261016')
RELEASE = 'newapi-concurrency-20261016'
VERSION = 'v0.13.2-carpool-r15.14-concurrency-20261016'
IMAGE = 'hzqicoding/new-api:carpool-r15.14-concurrency-20261016'
BASE_TAG = 'hzqicoding/new-api:concurrency-base-20261016'
OLD = 'sha256:7ce5c41556333571245e1bf3d36f788c0a85d4da35e2351d76b359b347aeb736'  # r15.13
POLICY_KEY = 'CarpoolConcurrencyPolicy'

spec = importlib.util.spec_from_file_location('release_base', '/opt/carpool/releases/sensitive-message-20260922/deploy.py')
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
ops = base.ops
base.ROOT = ops.ROOT = ROOT
base.RELEASE = ops.RELEASE = RELEASE
base.IMAGE, base.OLD, base.VERSION = IMAGE, OLD, VERSION
base.BINARY = hashlib.sha256((ROOT / 'new-api').read_bytes()).hexdigest()


def read(url):
    with urllib.request.urlopen(url, timeout=20) as r:
        assert r.status == 200
        return r.read()


def options_digest():
    schema = ops.schema_from_dsn(ops.envmap(ops.inspect('new-api'))['SQL_DSN'])
    # Hashes only; option values and credentials never leave the host.
    raw = ops.mysql('SELECT `key`,SHA2(value,256) FROM options ORDER BY `key`;', schema)
    return hashlib.sha256(raw.encode()).hexdigest()


def frontend_check(url):
    manifest = ops.load('frontend-manifest.json')['files']
    html = read(url + '/').decode()
    assert '/assets/codex-routes-20261005/' in html, 'served index does not reference the r15.13 asset namespace'
    checked = 1
    for path in sorted(p for p in manifest if p.startswith('web/classic/dist/assets/codex-routes-20261005/'))[:6]:
        content = read(url + path[len('web/classic/dist'):])
        assert hashlib.sha256(content).hexdigest() == manifest[path], path
        checked += 1
    return checked


def prepare():
    current = ops.inspect('new-api')
    assert current['Image'] == OLD, 'Production baseline changed'
    assert not (ROOT / 'production-before.json').exists(), 'Existing release needs review'
    container_ids = ops.run(['docker', 'ps', '-q']).decode().split()
    others = json.loads(ops.run(['docker', 'inspect'] + container_ids))
    ops.save('other-containers.json', {c['Name']: {'id': c['Id'], 'started': c['State']['StartedAt']} for c in others if c['Id'] != current['Id']})
    ops.save('production-before.json', {'containers': {'new-api': current}})
    ops.save('options-before-digest.json', {'sha256': options_digest()})
    (ROOT / 'index-before.html').write_bytes(read('http://127.0.0.1:7862/'))
    (ROOT / 'new-api').chmod(0o755)
    _, recovered = base.recover(ROOT / 'new-api')
    manifest = ops.load('frontend-manifest.json')['files']
    actual = {k: hashlib.sha256(v).hexdigest() for k, v in recovered.items()}
    assert actual == manifest, 'Embedded assets differ from r15.13 archive'
    ops.save('embedded-verification.json', {'passed': True, 'files': len(actual)})
    ops.run(['docker', 'tag', OLD, BASE_TAG])
    (ROOT / 'Dockerfile').write_text('FROM ' + BASE_TAG + '\nCOPY new-api /new-api\nLABEL carpool.release="' + RELEASE + '" carpool.binary.sha256="' + base.BINARY + '"\n')
    (ROOT / '.dockerignore').write_text('*\n!Dockerfile\n!new-api\n')
    ops.run(['docker', 'build', '--network=none', '--pull=false', '-t', IMAGE, str(ROOT)])
    image = json.loads(ops.run(['docker', 'image', 'inspect', IMAGE]))[0]['Id']
    ops.save('image.json', {'id': image, 'tag': IMAGE, 'binary': base.BINARY})
    print('Prepared image; embedded resources verified; production unchanged', flush=True)


def iso(dt):
    return dt.astimezone(timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.%fZ')


def policy(mode, user_id, limit, wait=0, issued=None, expires_in=600):
    issued = issued or datetime.now(timezone.utc)
    return json.dumps({'version': 1, 'issued_at': iso(issued), 'mode': mode, 'wait_seconds': wait,
                       'expires_at': iso(issued + timedelta(seconds=expires_in)), 'user_limits': {str(user_id): limit}})


def stage():
    name = 'newapi-concurrency-stage-20261016'
    assert ops.maybe_inspect(name) is None
    image = ops.load('image.json')['id']
    cfg = {'Image': image, 'Env': ['GIN_MODE=release', 'SESSION_SECRET=' + secrets.token_hex(32), 'CRYPTO_SECRET=' + secrets.token_hex(32), 'BATCH_UPDATE_ENABLED=false'],
           'Labels': {ops.LABEL: RELEASE, 'scope': 'isolated-concurrency-sqlite'},
           'HostConfig': {'NetworkMode': '1panel-network', 'Memory': 536870912, 'NanoCpus': 1000000000, 'PortBindings': {'3000/tcp': [{'HostIp': '127.0.0.1', 'HostPort': '17866'}]}},
           'ExposedPorts': {'3000/tcp': {}}}
    cid = ops.docker('POST', '/containers/create?name=' + name, cfg)['Id']
    seen = []
    delays = {'mock-fast': 0.0, 'mock-slow': 2.5}

    class Mock(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_POST(self):
            request = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            seen.append(request['model'])
            time.sleep(delays.get(request['model'], 0))
            raw = json.dumps({'id': 'cc-test', 'object': 'chat.completion', 'created': int(time.time()), 'model': request['model'],
                              'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': 'OK'}, 'finish_reason': 'stop'}],
                              'usage': {'prompt_tokens': 5, 'completion_tokens': 1, 'total_tokens': 6}}).encode()
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

    mock = ThreadingHTTPServer(('172.19.0.1', 0), Mock)
    threading.Thread(target=mock.serve_forever, daemon=True).start()
    results = {}
    try:
        ops.docker('POST', '/containers/' + cid + '/start')
        url = 'http://127.0.0.1:17866'
        ops.wait_http(url, '/api/status')
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        uid = None

        def call(method, path, body=None):
            status, result = ops.api(url, method, path, body, user_id=uid, opener=opener)
            assert status == 200 and result.get('success'), (path, status, result.get('message'))
            return result.get('data')

        pw = secrets.token_urlsafe(24)
        call('POST', '/api/setup', {'username': 'cc_stage', 'password': pw, 'confirmPassword': pw})
        uid = call('POST', '/api/user/login', {'username': 'cc_stage', 'password': pw})['id']
        call('PUT', '/api/option/', {'key': 'theme.frontend', 'value': 'classic'})
        status = call('GET', '/api/status')
        assert status['version'] == VERSION and status['theme'] == 'classic', status.get('version')
        call('PUT', '/api/option/', {'key': 'ModelRatio', 'value': json.dumps({'mock-fast': 0.1, 'mock-slow': 0.1})})
        call('POST', '/api/channel/', {'mode': 'single', 'channel': {'type': 1, 'name': 'mock', 'key': 'mock', 'base_url': 'http://172.19.0.1:' + str(mock.server_port),
                                                                    'models': 'mock-fast,mock-slow', 'group': 'default', 'status': 1}})
        created = call('POST', '/api/token/', {'name': 'cc', 'remain_quota': 0, 'unlimited_quota': True, 'expired_time': -1})
        token_id = created['id'] if isinstance(created, dict) and created.get('id') else call('GET', '/api/token/?p=1&size=10')['items'][0]['id']
        key = call('POST', '/api/token/' + str(token_id) + '/key')['key']
        key = key if key.startswith('sk-') else 'sk-' + key

        def relay(model):
            body = json.dumps({'model': model, 'messages': [{'role': 'user', 'content': 'hi'}]}).encode()
            req = urllib.request.Request(url + '/v1/chat/completions', data=body, method='POST',
                                         headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + key})
            started = time.time()
            try:
                with urllib.request.urlopen(req, timeout=30) as r:
                    return r.status, r.read().decode(), time.time() - started
            except urllib.error.HTTPError as e:
                return e.code, e.read().decode(), time.time() - started

        def set_policy(value):
            call('PUT', '/api/option/', {'key': POLICY_KEY, 'value': value})

        def pair(second_model='mock-fast', gap=0.4):
            """Start a slow request, then a second one while the first is in flight."""
            out = {}
            t = threading.Thread(target=lambda: out.__setitem__('first', relay('mock-slow')))
            t.start()
            time.sleep(gap)
            out['second'] = relay(second_model)
            t.join()
            return out['first'], out['second']

        # a) no policy key: unlimited
        first, second = pair()
        assert first[0] == 200 and second[0] == 200, ('no-policy', first[0], second[0], second[1][:200])
        results['no_policy'] = 'both 200'

        # b) enforce limit 1, no wait: second rejected with 429; slot released afterwards
        set_policy(policy('enforce', uid, 1, wait=0))
        first, second = pair()
        assert first[0] == 200 and second[0] == 429 and 'carpool_concurrency_limit' in second[1], ('enforce-reject', first[0], second[0], second[1][:200])
        after = relay('mock-fast')
        assert after[0] == 200, ('released', after[0], after[1][:200])
        results['enforce_reject'] = '429 then released'

        # c) enforce limit 1, wait 10s: second waits for the slot and succeeds
        set_policy(policy('enforce', uid, 1, wait=10))
        first, second = pair()
        assert first[0] == 200 and second[0] == 200 and second[2] >= 1.5, ('enforce-queue', first[0], second[0], round(second[2], 2))
        results['enforce_queue'] = 'second waited %.2fs then 200' % second[2]

        # d) observe: both pass, over-limit logged
        set_policy(policy('observe', uid, 1, wait=10))
        first, second = pair()
        assert first[0] == 200 and second[0] == 200, ('observe', first[0], second[0])
        logs = ops.run(['docker', 'logs', '--since', '2m', cid]).decode(errors='ignore')
        assert '[carpool-concurrency] observe over-limit user=%d' % uid in logs, 'observe log missing'
        results['observe'] = 'both 200, over-limit logged'

        # e) a fresh policy that is already expired: unlimited
        set_policy(json.dumps({'version': 1, 'issued_at': iso(datetime.now(timezone.utc)), 'mode': 'enforce', 'wait_seconds': 0,
                               'expires_at': iso(datetime.now(timezone.utc) - timedelta(seconds=1)), 'user_limits': {str(uid): 1}}))
        first, second = pair()
        assert first[0] == 200 and second[0] == 200, ('expired', first[0], second[0])
        results['expired'] = 'both 200'

        # f) ordering: newer off, then an older enforce must be ignored
        newer = datetime.now(timezone.utc)
        set_policy(policy('off', uid, 1, issued=newer))
        set_policy(policy('enforce', uid, 1, wait=0, issued=newer - timedelta(seconds=30)))
        first, second = pair()
        assert first[0] == 200 and second[0] == 200, ('ordering', first[0], second[0])
        results['ordering'] = 'older enforce ignored'

        results['assets'] = frontend_check(url)
        now = int(time.time())
        call('GET', '/api/user/self')
        for path in [f'/api/data/self?start_timestamp={now - 86400}&end_timestamp={now}', '/api/log/self/?p=1&page_size=1']:
            call('GET', path)
        results['dashboard_apis'] = True
        ops.save('stage-results.json', {'passed': True, 'image': image, 'checks': results, 'mock_requests': len(seen), 'browser_visual_test': False})
        print(json.dumps(results, ensure_ascii=False, indent=2), flush=True)
        print('Isolated concurrency, relay, assets and dashboard checks passed', flush=True)
    finally:
        mock.shutdown()
        mock.server_close()
        assert ops.inspect(cid)['Config']['Labels'].get('scope') == 'isolated-concurrency-sqlite'
        ops.docker('DELETE', '/containers/' + cid + '?force=true')


original_verify = base.verify_live


def verify_live():
    original_verify()
    assert read('http://127.0.0.1:7862/') == (ROOT / 'index-before.html').read_bytes(), 'live index changed'
    assets = frontend_check('http://127.0.0.1:7862')
    before = ops.load('other-containers.json')
    for name, expected in before.items():
        actual = ops.inspect(name.lstrip('/'))
        assert actual['Id'] == expected['id'] and actual['State']['StartedAt'] == expected['started'], name
    _, pricing = ops.api('http://127.0.0.1:7862', 'GET', '/api/pricing')
    luna = [p for p in pricing['data'] if p.get('group_ratio_override') is not None]
    assert luna and all(p['group_ratio_override'] == 1 for p in luna)
    options_same = options_digest() == ops.load('options-before-digest.json')['sha256']
    ops.save('final-verification.json', {'passed': True, 'assets': assets, 'options_unchanged': options_same,
                                         'other_containers_unchanged': True, 'luna_overrides': len(luna), 'browser_visual_test': False})
    print('Live frontend, Luna pricing and other services verified; options unchanged=%s' % options_same, flush=True)


base.verify_live = verify_live


def policy_off():
    """Emergency: write a fresh off policy through the admin API (takes effect immediately).

    Stop the carpool sync producer first (set concurrency_mode=off in carpool, or stop
    carpool-backend); otherwise the next carpool sync overwrites this.
    """
    env = ops.envmap(ops.inspect('carpool-backend'))
    now = datetime.now(timezone.utc)
    value = json.dumps({'version': 1, 'issued_at': iso(now), 'mode': 'off', 'wait_seconds': 10,
                        'expires_at': iso(now + timedelta(minutes=15)), 'user_limits': {}})
    auth = {'token': env['NEWAPI_ADMIN_TOKEN'], 'user_id': env['NEWAPI_ADMIN_USER_ID']}
    status, body = ops.api('http://127.0.0.1:7862', 'PUT', '/api/option/', {'key': POLICY_KEY, 'value': value}, **auth)
    assert status == 200 and body.get('success'), ('write failed', status, body.get('message'))
    status, body = ops.api('http://127.0.0.1:7862', 'GET', '/api/option/', **auth)
    stored = [o['value'] for o in body.get('data', []) if o.get('key') == POLICY_KEY]
    assert stored == [value], 'readback mismatch'
    print('Policy set to off (issued_at %s); memory readback matches' % iso(now), flush=True)


if __name__ == '__main__':
    {'prepare': prepare, 'stage': stage, 'cutover': base.cutover, 'verify': verify_live,
     'rollback': lambda: ops.rollback('new-api'), 'policy-off': policy_off}[sys.argv[1]]()
