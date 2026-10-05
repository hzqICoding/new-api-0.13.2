"""Frontend-only embedded-asset release; no production options writes."""
import hashlib
import http.cookiejar
import importlib.util
import json
import pathlib
import secrets
import sys
import time
import urllib.request

ROOT = pathlib.Path('/opt/carpool/releases/frontend-module-fix-20260925')
RELEASE = 'frontend-module-fix-20260925'
VERSION = 'v0.13.2-carpool-r15.8-frontend-20260925'
IMAGE = 'hzqicoding/new-api:carpool-r15.8-frontend-20260925'
OLD = 'sha256:d72738bc6b7aa16f1a9f3d6e4949e93a10427a24115547e103ebdf0953cb81c4'
NS = 'frontend-fix-20260925'
MAIN = 'index-luna-0216aa8f9328.js'
LEGACY = 'index-D4VkjDT5.js'

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
    # Return hashes only; SQL credentials and option values never leave host.
    raw = ops.mysql('SELECT `key`,SHA2(value,256) FROM options ORDER BY `key`;', schema)
    return hashlib.sha256(raw.encode()).hexdigest()


def frontend_check(url):
    manifest = ops.load('frontend-manifest.json')['files']
    html = read(url + '/').decode()
    assert '/assets/' + NS + '/' + MAIN in html
    paths = [MAIN, LEGACY, 'index-3m0hM-vt.js', 'react-core-D-iPSUlg.js', 'semi-ui-BzZ1UhpZ.js']
    for name in paths:
        path = '/assets/' + NS + '/' + name
        content = read(url + path)
        assert hashlib.sha256(content).hexdigest() == manifest['web/classic/dist' + path], path
    shim = read(url + '/assets/' + NS + '/' + LEGACY).decode()
    assert f'export * from "./{MAIN}";' in shim and len(shim) < 250
    assert 'group_ratio_override' in read(url + '/assets/' + NS + '/' + MAIN).decode()
    return len(paths)


def prepare():
    current = ops.inspect('new-api')
    assert current['Image'] == OLD, 'Production baseline changed'
    assert not (ROOT / 'production-before.json').exists(), 'Existing release needs review'
    container_ids = ops.run(['docker', 'ps', '-q']).decode().split()
    others = json.loads(ops.run(['docker', 'inspect'] + container_ids))
    ops.save('other-containers.json', {c['Name']: {'id': c['Id'], 'started': c['State']['StartedAt']} for c in others if c['Id'] != current['Id']})
    ops.save('production-before.json', {'containers': {'new-api': current}})
    ops.save('options-before-digest.json', {'sha256': options_digest()})
    old_html = read('http://127.0.0.1:7862/')
    (ROOT / 'index-before.html').write_bytes(old_html)
    (ROOT / 'new-api').chmod(0o755)
    # Verify actual embedded assets, not only the source-side tar archive.
    _, recovered = base.recover(ROOT / 'new-api')
    manifest = ops.load('frontend-manifest.json')['files']
    # recover returns per-path bytes in current extraction helper.
    actual = {k: hashlib.sha256(v).hexdigest() for k, v in recovered.items()}
    assert actual == manifest, 'Embedded assets differ from fixed archive'
    ops.save('embedded-verification.json', {'passed': True, 'files': len(actual)})
    ops.run(['docker', 'tag', OLD, 'hzqicoding/new-api:frontend-base-20260925'])
    (ROOT / 'Dockerfile').write_text('FROM hzqicoding/new-api:frontend-base-20260925\nCOPY new-api /new-api\nLABEL carpool.release="'+RELEASE+'" carpool.binary.sha256="'+base.BINARY+'"\n')
    (ROOT / '.dockerignore').write_text('*\n!Dockerfile\n!new-api\n')
    ops.run(['docker', 'build', '--network=none', '--pull=false', '-t', IMAGE, str(ROOT)])
    image = json.loads(ops.run(['docker', 'image', 'inspect', IMAGE]))[0]['Id']
    ops.save('image.json', {'id': image, 'tag': IMAGE, 'binary': base.BINARY})
    print('Prepared image; embedded resources verified; production unchanged', flush=True)


def stage():
    name = 'newapi-frontend-stage-20260925'
    assert ops.maybe_inspect(name) is None
    image = ops.load('image.json')['id']
    cfg = {'Image': image, 'Env': ['GIN_MODE=release', 'SESSION_SECRET='+secrets.token_hex(32), 'CRYPTO_SECRET='+secrets.token_hex(32), 'BATCH_UPDATE_ENABLED=false'],
           'Labels': {ops.LABEL: RELEASE, 'scope': 'isolated-frontend-sqlite'},
           'HostConfig': {'NetworkMode': 'bridge', 'Memory': 536870912, 'NanoCpus': 1000000000, 'PortBindings': {'3000/tcp': [{'HostIp': '127.0.0.1', 'HostPort': '17865'}]}},
           'ExposedPorts': {'3000/tcp': {}}}
    cid = ops.docker('POST', '/containers/create?name='+name, cfg)['Id']
    try:
        ops.docker('POST', '/containers/'+cid+'/start')
        url = 'http://127.0.0.1:17865'
        ops.wait_http(url, '/api/status')
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        uid = None

        def call(method, path, body=None):
            status, result = ops.api(url, method, path, body, user_id=uid, opener=opener)
            assert status == 200 and result.get('success'), (path, status, result.get('message'))
            return result.get('data')

        pw = secrets.token_urlsafe(24)
        call('POST', '/api/setup', {'username': 'fefix_stage', 'password': pw, 'confirmPassword': pw})
        uid = call('POST', '/api/user/login', {'username': 'fefix_stage', 'password': pw})['id']
        call('PUT', '/api/option/', {'key': 'theme.frontend', 'value': 'classic'})
        status = call('GET', '/api/status')
        assert status['version'] == VERSION and status['theme'] == 'classic'
        assets = frontend_check(url)
        call('GET', '/api/user/self')
        # Exercise dashboard data APIs on an isolated empty account.
        now = int(time.time())
        for path in [f'/api/data/self?start_timestamp={now-86400}&end_timestamp={now}', '/api/log/self/?p=1&page_size=1']:
            call('GET', path)
        ops.save('stage-results.json', {'passed': True, 'image': image, 'asset_checks': assets, 'isolated_login_and_dashboard_apis': True, 'browser_visual_test': False})
        print('Isolated login, dashboard APIs and asset serving passed', flush=True)
    finally:
        assert ops.inspect(cid)['Config']['Labels'].get('scope') == 'isolated-frontend-sqlite'
        ops.docker('DELETE', '/containers/'+cid+'?force=true')


original_verify = base.verify_live


def verify_live():
    original_verify()
    assets = frontend_check('http://127.0.0.1:7862')
    assert options_digest() == ops.load('options-before-digest.json')['sha256'], 'Production options changed; investigate concurrent changes'
    before = ops.load('other-containers.json')
    for name, expected in before.items():
        actual = ops.inspect(name.lstrip('/'))
        assert actual['Id'] == expected['id'] and actual['State']['StartedAt'] == expected['started'], name
    _, pricing = ops.api('http://127.0.0.1:7862', 'GET', '/api/pricing')
    luna = [p for p in pricing['data'] if p.get('group_ratio_override') is not None]
    assert luna and all(p['group_ratio_override'] == 1 for p in luna)
    ops.save('final-verification.json', {'passed': True, 'assets': assets, 'options_unchanged': True, 'other_containers_unchanged': True, 'luna_overrides': len(luna), 'browser_visual_test': False})
    print('Live frontend, Luna pricing, options and other services verified', flush=True)


base.verify_live = verify_live

if __name__ == '__main__':
    {'prepare': prepare, 'stage': stage, 'cutover': base.cutover, 'verify': verify_live,
     'rollback': lambda: ops.rollback('new-api')}[sys.argv[1]]()
