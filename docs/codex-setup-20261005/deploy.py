"""Pinned new-api release. Credentials and container settings stay on serverB."""
import hashlib
import http.cookiejar
import importlib.util
import json
import pathlib
import secrets
import sys
import urllib.request

ROOT = pathlib.Path('/opt/carpool/releases/newapi-codex-setup-20261005')
RELEASE = 'newapi-codex-setup-20261005'
VERSION = 'v0.13.2-carpool-r15.10-codex-setup-20261005'
IMAGE = 'hzqicoding/new-api:carpool-r15.10-codex-setup-20261005'
OLD = 'sha256:24d3bf6f7e1ee9df6654ff52fa3c358a3d19d4ac842cdf5ec84536dc530a3e9b'
NS = 'codex-setup-20261005'
MAIN = 'index-luna-0216aa8f9328.js'
spec = importlib.util.spec_from_file_location('release_base', '/opt/carpool/releases/sensitive-message-20260922/deploy.py')
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
ops = base.ops
base.ROOT = ops.ROOT = ROOT
base.RELEASE = ops.RELEASE = RELEASE
base.IMAGE, base.OLD, base.VERSION = IMAGE, OLD, VERSION
base.BINARY = hashlib.sha256((ROOT / 'new-api').read_bytes()).hexdigest()


def read(url):
    with urllib.request.urlopen(url, timeout=20) as response:
        assert response.status == 200
        return response.read()


def options_digest():
    schema = ops.schema_from_dsn(ops.envmap(ops.inspect('new-api'))['SQL_DSN'])
    rows = ops.mysql('SELECT `key`,SHA2(value,256) FROM options ORDER BY `key`;', schema)
    return hashlib.sha256(rows.encode()).hexdigest()


def frontend_check(url):
    manifest = ops.load('frontend-manifest.json')['files']
    html = read(url + '/').decode()
    assert f'/assets/{NS}/{MAIN}' in html and f'/assets/{NS}/codex-bootstrap.js' in html
    names = [name for name in manifest if name.startswith(f'web/classic/dist/assets/{NS}/codex-') and name.endswith('.js')]
    names += [f'web/classic/dist/assets/{NS}/{MAIN}', f'web/classic/dist/assets/{NS}/react-core-D-iPSUlg.js']
    for name in names:
        data = read(url + name.removeprefix('web/classic/dist'))
        assert hashlib.sha256(data).hexdigest() == manifest[name], 'Published asset hash mismatch'
    main = read(url + f'/assets/{NS}/{MAIN}').decode()
    assert 'group_ratio_override' in main and '__newapiOpenCodexSetup' in main
    return len(names)


def prepare():
    current = ops.inspect('new-api')
    assert current['Image'] == OLD, 'Production baseline changed'
    assert not (ROOT / 'production-before.json').exists(), 'Existing release needs review'
    others = json.loads(ops.run(['docker', 'inspect'] + ops.run(['docker', 'ps', '-q']).decode().split()))
    ops.save('other-containers.json', {c['Name']: {'id': c['Id'], 'started': c['State']['StartedAt']} for c in others if c['Id'] != current['Id']})
    ops.save('production-before.json', {'containers': {'new-api': current}})
    ops.save('options-before-digest.json', {'sha256': options_digest()})
    (ROOT / 'index-before.html').write_bytes(read('http://127.0.0.1:7862/'))
    (ROOT / 'new-api').chmod(0o755)
    _, recovered = base.recover(ROOT / 'new-api')
    actual = {name: hashlib.sha256(data).hexdigest() for name, data in recovered.items()}
    assert actual == ops.load('frontend-manifest.json')['files'], 'Embedded resource verification failed'
    ops.save('embedded-verification.json', {'passed': True, 'files': len(actual)})
    ops.run(['docker', 'tag', OLD, 'hzqicoding/new-api:codex-setup-base-20261005'])
    (ROOT / 'Dockerfile').write_text('FROM hzqicoding/new-api:codex-setup-base-20261005\nCOPY new-api /new-api\nLABEL carpool.release="'+RELEASE+'" carpool.binary.sha256="'+base.BINARY+'"\n')
    (ROOT / '.dockerignore').write_text('*\n!Dockerfile\n!new-api\n')
    ops.run(['docker', 'build', '--network=none', '--pull=false', '-t', IMAGE, str(ROOT)])
    image = json.loads(ops.run(['docker', 'image', 'inspect', IMAGE]))[0]['Id']
    ops.save('image.json', {'id': image, 'tag': IMAGE, 'binary': base.BINARY})
    print('Image prepared; complete embedded resources verified; production unchanged', flush=True)


def stage():
    """Targeted startup/static/owned-token contract checks in disposable SQLite.

    No upstream channel, wallet, paid model, production user or browser is used.
    """
    name = 'newapi-codex-setup-stage-20261005'
    assert ops.maybe_inspect(name) is None
    image = ops.load('image.json')['id']
    cfg = {'Image': image, 'Env': ['GIN_MODE=release', 'SESSION_SECRET='+secrets.token_hex(32), 'CRYPTO_SECRET='+secrets.token_hex(32), 'BATCH_UPDATE_ENABLED=false'],
           'Labels': {ops.LABEL: RELEASE, 'scope': 'isolated-codex-contract'},
           'HostConfig': {'NetworkMode': '1panel-network', 'Memory': 536870912, 'NanoCpus': 1000000000,
                          'PortBindings': {'3000/tcp': [{'HostIp': '127.0.0.1', 'HostPort': '17865'}]}},
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
            assert status == 200 and result.get('success'), 'Isolated API check failed: '+path
            return result.get('data')
        password = secrets.token_urlsafe(24)
        call('POST', '/api/setup', {'username': 'setupstage', 'password': password, 'confirmPassword': password})
        uid = call('POST', '/api/user/login', {'username': 'setupstage', 'password': password})['id']
        call('PUT', '/api/option/', {'key': 'theme.frontend', 'value': 'classic'})
        assert call('GET', '/api/status')['version'] == VERSION
        token = call('POST', '/api/token/', {'name': 'setup-contract-test', 'expired_time': -1, 'unlimited_quota': True, 'group': 'default'})
        assert token['id'] > 0 and '*' in token['key'], 'Create must return exact ID and masked key'
        meta = call('GET', f"/api/token/{token['id']}/codex-config")
        assert meta['token_id'] == token['id'] and meta['template_version'] == 'newapi-codex-v1'
        assert meta['configurable'] is False and meta['reason_code'] == 'no_compatible_model', 'Empty isolated model catalog must not advertise usable models'
        assert 'key' not in meta and 'user_id' not in meta
        status, _ = ops.api(url, 'GET', f"/api/token/{token['id']}/codex-config")
        assert status in (401, 403), 'Anonymous metadata access was not rejected'
        assets = frontend_check(url)
        ops.save('stage-results.json', {'passed': True, 'image': image, 'asset_checks': assets, 'token_create_metadata_anonymous_contract': True, 'real_model_calls': 0, 'browser_test': False})
        print('Isolated startup, token metadata/creation and static assets passed; no provider calls', flush=True)
    finally:
        assert ops.inspect(cid)['Config']['Labels'].get('scope') == 'isolated-codex-contract'
        ops.docker('DELETE', '/containers/'+cid+'?force=true&v=true')


original_verify = base.verify_live
def verify_live():
    original_verify()
    assets = frontend_check('http://127.0.0.1:7862')
    assert options_digest() == ops.load('options-before-digest.json')['sha256'], 'Production options changed'
    for name, expected in ops.load('other-containers.json').items():
        current = ops.inspect(name.lstrip('/'))
        assert current['Id'] == expected['id'] and current['State']['StartedAt'] == expected['started'], 'Unrelated service changed'
    status, _ = ops.api('http://127.0.0.1:7862', 'GET', '/api/token/1/codex-config')
    assert status in (401, 403)
    _, pricing = ops.api('http://127.0.0.1:7862', 'GET', '/api/pricing')
    luna = [item for item in pricing['data'] if item.get('group_ratio_override') is not None]
    assert luna and all(item['group_ratio_override'] == 1 for item in luna)
    result = {'passed': True, 'version': VERSION, 'image': ops.inspect('new-api')['Image'], 'binary_sha256': base.BINARY,
              'assets_verified': assets, 'options_unchanged': True, 'other_containers_unchanged': True,
              'anonymous_config_rejected': True, 'luna_overrides': len(luna), 'browser_test': False}
    ops.save('final-verification.json', result)
    print(json.dumps(result), flush=True)

base.verify_live = verify_live
if __name__ == '__main__':
    {'prepare': prepare, 'stage': stage, 'cutover': base.cutover, 'verify': verify_live, 'rollback': lambda: ops.rollback('new-api')}[sys.argv[1]]()
