"""r15.17 default-disabled release; run only on serverB as the release operator.

Inputs in ROOT: new-api, frontend-manifest.json, build-manifest.json.
Commands: preflight, prepare, cutover, verify, rollback.
There is intentionally no stage, migration, policy-write or feature-enable action.
"""
import copy
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import sys
import time
import urllib.request

ROOT = pathlib.Path('/opt/carpool/releases/newapi-usage-aggregate-20261019')
PREVIOUS = pathlib.Path('/opt/carpool/releases/newapi-failure-log-20261018')
RELEASE = 'newapi-usage-aggregate-20261019'
VERSION = 'v0.13.2-carpool-r15.17-usage-aggregate-20261019'
IMAGE = 'hzqicoding/new-api:carpool-r15.17-usage-aggregate-20261019'
BASE_TAG = 'hzqicoding/new-api:usage-aggregate-base-20261019'
OLD = 'sha256:45a4d257a4f048bdca4c60b12e77881f2814425b9f0222ef5d8409ff1174fb18'
OLD_BINARY = 'c987f75bb5db4a72119951da575351c34d7b2c17f7f5369ccdcc5c7d6064afd6'
OLD_VERSION = 'v0.13.2-carpool-r15.16-failure-log-20261018'
URL = 'http://127.0.0.1:7862'
FLAG = 'CARPOOL_USAGE_AGGREGATE_ENABLED'
ADDED = {
    'controller/usage_aggregate.go', 'controller/usage_aggregate_test.go',
    'model/usage_aggregate.go', 'model/usage_aggregate_test.go',
    'router/usage_aggregate_test.go',
}
CHANGED = {'router/api-router.go'}

# Reuse only the established read/inspect/container/rollback primitives. The old
# base.cutover requires a business stage receipt, so this release does NOT call it.
spec = importlib.util.spec_from_file_location(
    'usage_release_base', '/opt/carpool/releases/sensitive-message-20260922/deploy.py')
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
ops = base.ops
base.ROOT = ops.ROOT = ROOT
base.RELEASE = ops.RELEASE = RELEASE
base.IMAGE, base.OLD, base.VERSION = IMAGE, OLD, VERSION


def require(ok, message):
    if not ok:
        raise RuntimeError(message)


def sha_file(path):
    with pathlib.Path(path).open('rb') as stream:
        digest = hashlib.sha256()
        for part in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(part)
    return digest.hexdigest()


def read(path):
    with urllib.request.urlopen(URL + path, timeout=20) as response:
        require(response.status == 200, 'static resource HTTP status failed')
        return response.read()


def disabled(container):
    values = [entry.split('=', 1)[1] for entry in container['Config']['Env']
              if entry.startswith(FLAG + '=')]
    require(all(value.strip().lower() == 'false' for value in values),
            'aggregate flag must be absent or false; refusing feature activation')


def running_others():
    ids = ops.run(['docker', 'ps', '-q']).decode().split()
    containers = json.loads(ops.run(['docker', 'inspect'] + ids)) if ids else []
    return {item['Name']: {'id': item['Id'], 'image': item['Image'],
                           'started': item['State']['StartedAt'],
                           'restarts': item.get('RestartCount', 0)}
            for item in containers if item['Name'] != '/new-api'}


def options_digest():
    schema = ops.schema_from_dsn(ops.envmap(ops.inspect('new-api'))['SQL_DSN'])
    # The existing carpool job refreshes this policy periodically. No other
    # option is excluded, and this release never writes any option.
    text = ops.mysql("SELECT `key`,SHA2(value,256) FROM options "
                     "WHERE `key` <> 'CarpoolConcurrencyPolicy' ORDER BY `key`;", schema)
    return hashlib.sha256(text.encode()).hexdigest()


def schema_digests():
    env = ops.envmap(ops.inspect('new-api'))
    schemas = {ops.schema_from_dsn(env[key]) for key in ('SQL_DSN', 'LOG_SQL_DSN') if env.get(key)}
    result = {}
    for schema in sorted(schemas):
        columns = ops.mysql("SELECT TABLE_NAME,COLUMN_NAME,COLUMN_TYPE,IS_NULLABLE,"
                            "COALESCE(COLUMN_DEFAULT,'<NULL>'),EXTRA FROM information_schema.COLUMNS "
                            "WHERE TABLE_SCHEMA=DATABASE() ORDER BY TABLE_NAME,ORDINAL_POSITION;", schema)
        indexes = ops.mysql("SELECT TABLE_NAME,INDEX_NAME,SEQ_IN_INDEX,COLUMN_NAME,NON_UNIQUE,INDEX_TYPE "
                            "FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() "
                            "ORDER BY TABLE_NAME,INDEX_NAME,SEQ_IN_INDEX;", schema)
        result[schema] = hashlib.sha256((columns + '\n' + indexes).encode()).hexdigest()
    return result


def check_manifest():
    record = ops.load('build-manifest.json')
    require(record.get('version') == VERSION and record.get('baseline_image') == OLD,
            'candidate build manifest baseline/version mismatch')
    require(record.get('baseline_binary_sha256') == OLD_BINARY,
            'build manifest r15.16 executable baseline mismatch')
    binary = sha_file(ROOT / 'new-api')
    require(record.get('binary_sha256') == binary, 'candidate binary hash mismatch')
    old, new = record.get('baseline_source_files'), record.get('source_files')
    require(isinstance(old, dict) and isinstance(new, dict) and len(old) >= 500,
            'full baseline/candidate Go source manifests required')
    for files in (old, new):
        for name, digest in files.items():
            relative = pathlib.PurePosixPath(name)
            require(not relative.is_absolute() and '..' not in relative.parts and
                    (name.endswith('.go') or name in ('go.mod', 'go.sum')) and
                    isinstance(digest, str) and re.fullmatch('[0-9a-f]{64}', digest),
                    'invalid source manifest entry')
    require(set(old) - set(new) == set(), 'baseline source files were removed')
    require(set(new) - set(old) == ADDED, 'unexpected added Go sources')
    require({name for name in old if old[name] != new[name]} == CHANGED,
            'unexpected existing Go source changes')
    require(all(name in old and old[name] == new[name] for name in ('go.mod', 'go.sum')),
            'dependency definitions changed')
    old_assets = json.loads((PREVIOUS / 'frontend-manifest.json').read_text())['files']
    assets = ops.load('frontend-manifest.json')['files']
    require(assets == old_assets and len(assets) == 956, 'frontend manifest changed from r15.16')
    base.BINARY = binary
    return binary, assets


def preflight():
    current = ops.inspect('new-api')
    require(current['Image'] == OLD and current['State']['Running'], 'production r15.16 image/running baseline changed')
    require(ops.run(['docker', 'exec', 'new-api', 'sha256sum', '/proc/1/exe']).decode().split()[0] == OLD_BINARY,
            'production r15.16 executable baseline changed')
    status, body = ops.api(URL, 'GET', '/api/status')
    require(status == 200 and body.get('data', {}).get('version') == OLD_VERSION, 'production r15.16 version changed')
    disabled(current)
    require(all(mount['Type'] == 'bind' for mount in current['Mounts']), 'unexpected volume mount requires review')
    require(ops.maybe_inspect('new-api-candidate-' + RELEASE) is None and
            ops.maybe_inspect('new-api-rollback-' + RELEASE) is None, 'release container names already exist')
    binary, assets = check_manifest()
    require(ops.api('http://127.0.0.1:18110', 'GET', '/health')[0] == 200, 'carpool health failed')
    print(json.dumps({'preflight': True, 'baseline_image': OLD, 'candidate_binary': binary,
                      'embedded_manifest_files': len(assets), 'aggregate_enabled': False}), flush=True)
    return current, binary, assets


def prepare():
    require(not (ROOT / 'production-before.json').exists() and not (ROOT / 'deployment-state.json').exists(),
            'release already prepared; inspect its recorded state')
    current, binary, assets = preflight()
    ROOT.chmod(0o700)
    _, recovered = base.recover(ROOT / 'new-api')
    actual = {name: hashlib.sha256(content).hexdigest() for name, content in recovered.items()}
    require(actual == assets, 'candidate embedded resources changed from r15.16')
    # No synthetic stage-results.json is created. These are actual build/source
    # and embedded-asset checks, separate from the already-run local tests.
    ops.save('production-before.json', {'containers': {'new-api': current}})
    ops.save('other-containers.json', running_others())
    ops.save('options-before-digest.json', {'sha256': options_digest()})
    ops.save('schema-before-digests.json', schema_digests())
    (ROOT / 'index-before.html').write_bytes(read('/'))
    (ROOT / 'new-api').chmod(0o755)
    # Resolve the exact local image ID into the established builder-compatible
    # base tag, then verify it. Never pull a potentially different registry base.
    ops.run(['docker', 'tag', OLD, BASE_TAG])
    require(json.loads(ops.run(['docker', 'image', 'inspect', BASE_TAG]))[0]['Id'] == OLD,
            'base image tag did not resolve to the pinned r15.16 image')
    (ROOT / 'Dockerfile').write_text('FROM ' + BASE_TAG + '\nCOPY new-api /new-api\nLABEL carpool.release="' +
                                   RELEASE + '" carpool.binary.sha256="' + binary + '"\n')
    (ROOT / '.dockerignore').write_text('*\n!Dockerfile\n!new-api\n')
    ops.run(['docker', 'build', '--network=none', '--pull=false', '-t', IMAGE, str(ROOT)])
    image = json.loads(ops.run(['docker', 'image', 'inspect', IMAGE]))[0]['Id']
    ops.save('image.json', {'id': image, 'tag': IMAGE, 'binary': binary})
    ops.save('build-checks.json', {'passed': True, 'image': image, 'binary': binary,
                                 'build_manifest_sha256': sha_file(ROOT / 'build-manifest.json'),
                                 'frontend_manifest_sha256': sha_file(ROOT / 'frontend-manifest.json'),
                                 'embedded_files': len(actual), 'stage_executed': False})
    print('Prepared r15.17 image; source and 956 embedded assets verified; production unchanged', flush=True)


def verify_live():
    binary, assets = check_manifest()
    image = ops.load('image.json')
    current = ops.inspect('new-api')
    require(current['Image'] == image['id'], 'live candidate image mismatch')
    disabled(current)
    # Exactly the existing one-row token-scoped old API check plus status,
    # executable hash and carpool health. No relay or monetary operation.
    base.verify_live()
    require(read('/') == (ROOT / 'index-before.html').read_bytes(), 'served index changed')
    paths = sorted(name for name in assets if name.startswith('web/classic/dist/assets/codex-routes-20261005/'))[:6]
    # Keep an old entry point in the static probes as well as current assets.
    legacy = 'web/classic/dist/assets/index-D4VkjDT5.js'
    if legacy in assets:
        paths.append(legacy)
    for name in paths:
        require(hashlib.sha256(read(name[len('web/classic/dist'):])).hexdigest() == assets[name],
                'served static asset checksum mismatch')
    env = ops.envmap(ops.inspect('carpool-backend'))
    body = {'version': 1, 'tokens': [], 'from_unix': None, 'to_unix': None,
            'after_log_id': 0, 'through_log_id': 0}
    status, value = ops.api(URL, 'POST', '/api/log/usage/aggregate', body,
                            token=env['NEWAPI_ADMIN_TOKEN'], user_id=env['NEWAPI_ADMIN_USER_ID'])
    require(status == 503 and value.get('success') is False and value.get('message') == 'usage_aggregate_disabled',
            'authorized aggregate endpoint must remain disabled')
    status, _ = ops.api(URL, 'POST', '/api/log/usage/aggregate', body)
    require(status == 401, 'anonymous aggregate request must remain unauthorized')
    require(options_digest() == ops.load('options-before-digest.json')['sha256'], 'non-policy production options changed')
    require(schema_digests() == ops.load('schema-before-digests.json'), 'production schema/index definitions changed')
    require(running_others() == ops.load('other-containers.json'), 'unrelated running containers changed')
    ops.save('final-verification.json', {'passed': True, 'version': VERSION, 'image': current['Image'],
                                       'binary_sha256': binary, 'aggregate_enabled': False,
                                       'authorized_post_status': 503, 'anonymous_post_status': 401,
                                       'old_log_token_filter': True, 'static_probes': len(paths) + 1,
                                       'options_unchanged_except_concurrency_policy': True,
                                       'schema_unchanged': True, 'other_containers_unchanged': True,
                                       'stage_executed': False, 'browser_test': False})
    print('Verified r15.17 disabled API, old log contract, unchanged assets/options/schema/other containers', flush=True)


def cutover():
    before = ops.load('production-before.json')['containers']['new-api']
    current, binary, _ = preflight()
    require(current['Id'] == before['Id'], 'production container changed after prepare')
    require(current['Config'] == before['Config'] and current['HostConfig'] == before['HostConfig'] and
            current['Mounts'] == before['Mounts'] and ops.endpoints(current, 'new-api') == ops.endpoints(before, 'new-api'),
            'production container configuration changed after prepare')
    image, checked = ops.load('image.json'), ops.load('build-checks.json')
    require(checked.get('passed') is True and checked['image'] == image['id'] and
            checked['binary'] == binary and image['binary'] == binary and
            checked['build_manifest_sha256'] == sha_file(ROOT / 'build-manifest.json') and
            checked['frontend_manifest_sha256'] == sha_file(ROOT / 'frontend-manifest.json'),
            'candidate build checks changed after prepare')
    require(running_others() == ops.load('other-containers.json'), 'unrelated containers changed after prepare')
    require(options_digest() == ops.load('options-before-digest.json')['sha256'], 'non-policy options changed before cutover')
    require(schema_digests() == ops.load('schema-before-digests.json'), 'schema/index definitions changed before cutover')
    require(not (ROOT / 'deployment-state.json').exists(), 'existing cutover state requires review')
    state = {'new-api': {'old_id': before['Id'], 'new_id': None,
                         'candidate_name': 'new-api-candidate-' + RELEASE, 'expected_image': image['id']}}
    ops.save('deployment-state.json', state)
    try:
        config = copy.deepcopy(before['Config'])
        config['Image'] = image['id']
        config['Labels'] = dict(config.get('Labels') or {}, **{ops.LABEL: RELEASE, 'carpool.binary.sha256': binary})
        config['HostConfig'] = copy.deepcopy(before['HostConfig'])
        config['NetworkingConfig'] = {'EndpointsConfig': ops.endpoints(before, 'new-api')}
        cid = ops.docker('POST', '/containers/create?name=' + state['new-api']['candidate_name'], config)['Id']
        state['new-api']['new_id'] = cid
        ops.save('deployment-state.json', state)
        candidate = ops.inspect(cid)
        for key in ('Env', 'Entrypoint', 'Cmd', 'WorkingDir', 'User', 'Healthcheck', 'ExposedPorts', 'Volumes', 'StopSignal', 'Tty', 'OpenStdin'):
            require(candidate['Config'].get(key) == before['Config'].get(key), 'candidate container config drift: ' + key)
        for key in ('Binds', 'PortBindings', 'RestartPolicy', 'Memory', 'MemorySwap', 'NanoCpus', 'CpuShares', 'CpuQuota', 'CpuPeriod', 'CpusetCpus', 'NetworkMode', 'ExtraHosts', 'LogConfig', 'Privileged', 'ReadonlyRootfs', 'SecurityOpt', 'CapAdd', 'CapDrop', 'PidsLimit', 'Ulimits', 'ShmSize'):
            require(candidate['HostConfig'].get(key) == before['HostConfig'].get(key), 'candidate host config drift: ' + key)
        mounts = lambda item: sorted((m['Type'], m['Source'], m['Destination'], m['RW']) for m in item['Mounts'])
        require(mounts(candidate) == mounts(before), 'candidate mounts changed')
        disabled(candidate)
        ops.docker('POST', '/containers/' + before['Id'] + '/stop?t=30')
        ops.docker('POST', '/containers/' + before['Id'] + '/rename?name=new-api-rollback-' + RELEASE)
        for network in before['NetworkSettings']['Networks']:
            ops.docker('POST', '/networks/' + network + '/disconnect', {'Container': before['Id'], 'Force': True})
        ops.docker('POST', '/containers/' + cid + '/rename?name=new-api')
        ops.docker('POST', '/containers/' + cid + '/start')
        ops.wait_http(URL, '/api/status')
        verify_live()
        state['new-api']['healthy'] = True
        state['new-api']['finished_at'] = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
        ops.save('deployment-state.json', state)
        print('Cutover verified; r15.16 container retained; aggregation remains disabled', flush=True)
    except Exception:
        rollback()
        raise


def rollback():
    ops.rollback('new-api')
    ops.wait_http(URL, '/api/status')
    current = ops.inspect('new-api')
    require(current['Image'] == OLD, 'rollback image mismatch')
    disabled(current)
    require(ops.run(['docker', 'exec', 'new-api', 'sha256sum', '/proc/1/exe']).decode().split()[0] == OLD_BINARY,
            'rollback executable mismatch')
    require(ops.api(URL, 'GET', '/api/status')[1].get('data', {}).get('version') == OLD_VERSION,
            'rollback version mismatch')
    print('r15.16 container/config/network restored; no database restoration or business writes performed', flush=True)


if __name__ == '__main__':
    os.umask(0o077)
    commands = {'preflight': preflight, 'prepare': prepare, 'cutover': cutover, 'verify': verify_live, 'rollback': rollback}
    if len(sys.argv) != 2 or sys.argv[1] not in commands:
        raise SystemExit('usage: deploy.py preflight|prepare|cutover|verify|rollback')
    commands[sys.argv[1]]()
