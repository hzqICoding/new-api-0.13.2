"""r15.18 software-only release, executed by the operator on serverB.

prepare -> cutover -> verify; rollback is a separate, billing-guarded action.
This script never writes options, migrates Luna, calls a model or edits compose.
Only the exact container created by this release can be removed on recovery.
"""
import copy
import hashlib
import importlib.util
import json
import math
import os
import pathlib
import re
import sys
import time
from datetime import datetime, timezone
from urllib.request import urlopen

ROOT = pathlib.Path('/opt/carpool/releases/newapi-carpool-billing-20261020')
PREVIOUS = pathlib.Path('/opt/carpool/releases/newapi-usage-aggregate-20261019')
RELEASE = ROOT.name
VERSION = 'v0.13.2-carpool-r15.18-billing-20261020'
IMAGE = 'hzqicoding/new-api:carpool-r15.18-billing-20261020'
BASE_TAG = 'hzqicoding/new-api:carpool-billing-base-20261020'
OLD_ID = 'bd726f2b1f6e5d336a4391671b1b00336663bf88afa5220e60dfa41180f2ce63'
OLD = 'sha256:82ffa348e9abe6f352253a56eed6737afb2e480a5e83ae78b9580526ca0af28b'
OLD_BINARY = '756970f5ee85edef29eaada69bad653303e1443cb1029b5d0e4b3d2277764e3d'
OLD_VERSION = 'v0.13.2-carpool-r15.17-usage-aggregate-20261019'
OLD_STARTED = '2026-10-19T12:31:59.324728811Z'
NAME = 'new-api'
CANDIDATE = NAME + '-candidate-' + RELEASE
BACKUP = NAME + '-rollback-' + RELEASE
URL = 'http://127.0.0.1:7862'
CP_HEALTH = 'http://127.0.0.1:18110'
LOCK = pathlib.Path('/opt/carpool/releases/.newapi-software-release.lock')
FLAG = 'CARPOOL_USAGE_AGGREGATE_ENABLED'
POLICY = 'CarpoolBillingPolicy'
LABEL = 'carpool.release'
EXPECTED_DEPENDENCIES = 114
ROW_PRICING_VERSION = '5a90f2b86c08bd983a9a2e6d66c255f4eaef9c4bc934386d2b6ae84ef0ff1f1f'

# Reviewed delta supplied by the main agent; never inferred from a candidate.
ADDED = {
    'controller/carpool_pricing_test.go',
    'relay/helper/carpool_billing_test.go',
    'service/carpool_billing_test.go',
    'setting/billing_setting/carpool_policy.go',
    'setting/billing_setting/carpool_policy_test.go',
}
CHANGED = {
    'controller/relay.go',
    'model/task.go',
    'relay/common/relay_info.go',
    'relay/helper/price.go',
    'relay/helper/stream_scanner_test.go',
    'relay/mjproxy_handler.go',
    'service/channel_affinity_usage_cache_test.go',
    'service/log_info_generate.go',
    'service/quota.go',
    'service/task_billing.go',
    'service/violation_fee.go',
    'types/price_data.go',
}
ops = None
recover = None


class GuardError(RuntimeError):
    """Static, credential-free operator diagnostic."""


def require(value, message):
    if not value:
        raise GuardError(message)


def stamp():
    return datetime.now(timezone.utc).isoformat()


def sha_file(path):
    digest = hashlib.sha256()
    with pathlib.Path(path).open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(block)
    return digest.hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), allow_nan=False).encode()


def load(name):
    path = ROOT / name
    require(path.parent == ROOT and path.is_file() and not path.is_symlink(), 'missing or unsafe release input: ' + name)
    require(path.stat().st_mode & 0o077 == 0, 'release inputs must be owner-only: ' + name)
    return json.loads(path.read_text(encoding='utf-8'))


def save(name, value):
    path = ROOT / name
    require(path.parent == ROOT and not path.is_symlink(), 'unsafe release receipt')
    temp = path.with_name(path.name + '.tmp')
    require(not temp.is_symlink(), 'unsafe temporary receipt')
    with temp.open('w', encoding='utf-8') as stream:
        os.fchmod(stream.fileno(), 0o600)
        json.dump(value, stream, indent=2, allow_nan=False)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temp, path)


def state():
    return load('deployment-state.json') if (ROOT / 'deployment-state.json').exists() else {}


def envmap(container):
    result = {}
    for entry in container['Config']['Env']:
        key, sep, value = entry.partition('=')
        require(sep and key not in result, 'duplicate or malformed container environment')
        result[key] = value
    return result


def host_config(config):
    result = copy.deepcopy(config)
    if result.get('OomKillDisable') is None:
        result['OomKillDisable'] = False
    return result


def endpoints(container):
    result = ops.endpoints(container, NAME)
    for network, detail in container['NetworkSettings']['Networks'].items():
        for key in ('Links', 'DriverOpts', 'GwPriority'):
            if detail.get(key):
                result[network][key] = copy.deepcopy(detail[key])
    return result


def assert_config(current, before, new=False, network=True):
    left, right = copy.deepcopy(current['Config']), copy.deepcopy(before['Config'])
    for config in (left, right):
        config.pop('Image', None)
        config.pop('Labels', None)
    require(left == right, 'container configuration or environment changed')
    require(host_config(current['HostConfig']) == host_config(before['HostConfig']), 'HostConfig changed')
    mounts = lambda c: sorted((m['Type'], m['Source'], m['Destination'], m['RW'], m.get('Mode'), m.get('Propagation')) for m in c['Mounts'])
    require(mounts(current) == mounts(before), 'container mounts changed')
    if network:
        actual = endpoints(current)
        for endpoint in actual.values():
            endpoint['Aliases'] = [alias for alias in endpoint.get('Aliases', []) if alias not in (CANDIDATE, BACKUP)]
        require(actual == endpoints(before), 'container network configuration changed')
    labels = dict(before['Config'].get('Labels') or {})
    if new:
        labels.update({LABEL: RELEASE, 'carpool.binary.sha256': load('image.json')['binary'],
                       'carpool.rollout.old_id': before['Id'], 'carpool.rollout.service': NAME})
    require((current['Config'].get('Labels') or {}) == labels, 'container ownership labels changed')


def read(path):
    with urlopen(URL + path, timeout=20) as response:
        require(response.status == 200, 'public HTTP resource check failed')
        return response.read()


def pricing_snapshot():
    raw = read('/api/pricing')
    value = json.loads(raw)
    require(isinstance(value, dict) and value.get('success') is True and isinstance(value.get('data'), list), 'pricing response invalid')
    value = copy.deepcopy(value)
    models, markers = set(), []
    for row in value['data']:
        require(isinstance(row, dict) and isinstance(row.get('model_name'), str) and row['model_name'] not in models,
                'duplicate or invalid pricing model identity')
        models.add(row['model_name'])
        if 'pricing_version' in row:
            markers.append(row.pop('pricing_version'))
        # These are sets in the producer. Sorting retains duplicate counts.
        for key in ('enable_groups', 'supported_endpoint_types'):
            values = row.get(key)
            require(isinstance(values, list) and all(isinstance(v, str) for v in values), 'pricing set field invalid')
            row[key] = sorted(values)
    # The controller filters usable groups AFTER model.GetPricing places the
    # marker on a random first model. That row may be absent publicly. Both
    # producer files are pinned unchanged by check_manifest; validate any
    # visible marker against their constant, not its random presence/location.
    require(len(markers) <= 1 and all(v == ROW_PRICING_VERSION for v in markers), 'pricing row version marker changed')
    value['data'].sort(key=lambda row: row['model_name'])
    vendors = value.get('vendors')
    require(isinstance(vendors, list), 'pricing vendors missing')
    vendor_ids = [row.get('id') for row in vendors if isinstance(row, dict)]
    require(len(vendor_ids) == len(vendors) and all(type(i) is int for i in vendor_ids) and len(set(vendor_ids)) == len(vendor_ids),
            'duplicate or invalid pricing vendor identity')
    value['vendors'].sort(key=lambda row: row['id'])
    # model/pricing.go places this same cache marker on a nondeterministic first
    # model. Its value/count is checked, but its physical row is not pricing.
    return {'raw_sha256': hashlib.sha256(raw).hexdigest(), 'row_pricing_version': markers[0] if markers else None,
            'row_pricing_marker_count': len(markers),
            'semantic': value, 'semantic_sha256': hashlib.sha256(canonical(value)).hexdigest()}


def sql_read(sql, schema):
    prefix = "SET SESSION max_execution_time=3000; SET time_zone='+00:00'; START TRANSACTION READ ONLY; "
    return ops.mysql(prefix + sql + ' COMMIT;', schema)


def option_snapshot():
    schema = ops.schema_from_dsn(envmap(ops.inspect(NAME))['SQL_DSN'])
    raw = sql_read("SELECT JSON_OBJECT('key',`key`,'sha256',SHA2(value,256)) FROM options ORDER BY `key`;", schema)
    values = {}
    for line in raw.splitlines():
        row = json.loads(line)
        require(row['key'] not in values, 'duplicate production option')
        values[row['key']] = row['sha256']
    dynamic = values.pop('CarpoolConcurrencyPolicy', None)
    return {'stable': values, 'concurrency_policy_sha256': dynamic}


def schema_digests():
    environment = envmap(ops.inspect(NAME))
    schemas = {ops.schema_from_dsn(environment[key]) for key in ('SQL_DSN', 'LOG_SQL_DSN') if environment.get(key)}
    result = {}
    for schema in sorted(schemas):
        columns = sql_read("SELECT TABLE_NAME,COLUMN_NAME,COLUMN_TYPE,IS_NULLABLE,COALESCE(COLUMN_DEFAULT,'<NULL>'),EXTRA "
                           "FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() ORDER BY TABLE_NAME,ORDINAL_POSITION;", schema)
        indexes = sql_read("SELECT TABLE_NAME,INDEX_NAME,SEQ_IN_INDEX,COLUMN_NAME,NON_UNIQUE,INDEX_TYPE "
                           "FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() ORDER BY TABLE_NAME,INDEX_NAME,SEQ_IN_INDEX;", schema)
        result[schema] = hashlib.sha256((columns + '\n' + indexes).encode()).hexdigest()
    return result


def running_others():
    ids = ops.run(['docker', 'ps', '-q']).decode().split()
    items = json.loads(ops.run(['docker', 'inspect'] + ids)) if ids else []
    return {item['Name']: {'id': item['Id'], 'image': item['Image'], 'started': item['State']['StartedAt'],
                           'restarts': item.get('RestartCount', 0), 'config_sha256': hashlib.sha256(canonical(item['Config'])).hexdigest(),
                           'host_sha256': hashlib.sha256(canonical(host_config(item['HostConfig']))).hexdigest(),
                           'mounts_sha256': hashlib.sha256(canonical(sorted(item['Mounts'], key=canonical))).hexdigest(),
                           'networks_sha256': hashlib.sha256(canonical(item['NetworkSettings']['Networks'])).hexdigest()}
            for item in items if item['Name'].lstrip('/') not in (NAME, CANDIDATE, BACKUP)}


def unfinished_task_guard(schema):
    exists = sql_read("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='tasks';", schema).strip()
    require(exists == '1', 'expected r15.17 tasks table is missing; cannot establish rollback billing safety')
    # A SQL NULL is the known legacy representation of an empty TaskPrivateData
    # (model/task.go Value). Non-object/malformed JSON or an invalid context is
    # unknown. Return only two boolean facts, never private_data or its keys.
    query = """SELECT JSON_OBJECT(
        'invalid', CASE
          WHEN private_data IS NULL THEN 0
          WHEN JSON_VALID(private_data)=0 THEN 1
          WHEN JSON_TYPE(private_data)<>'OBJECT' THEN 1
          WHEN JSON_CONTAINS_PATH(private_data,'one','$.billing_context')=0 THEN 0
          WHEN JSON_TYPE(JSON_EXTRACT(private_data,'$.billing_context'))<>'OBJECT' THEN 1
          ELSE 0 END,
        'snapshot', CASE
          WHEN private_data IS NULL THEN 0
          WHEN JSON_VALID(private_data)=0 THEN 0
          WHEN JSON_TYPE(private_data)<>'OBJECT' THEN 0
          ELSE JSON_CONTAINS_PATH(private_data,'one','$.billing_context.carpool_snapshot') END)
        FROM tasks WHERE status IS NULL OR BINARY status NOT IN ('SUCCESS','FAILURE')
        ORDER BY id LIMIT 1001;"""
    lines = sql_read(query, schema).splitlines()
    require(len(lines) <= 1000, 'unfinished task scope exceeds rollback inspection budget; wait for settlement and retry')
    for line in lines:
        try:
            row = json.loads(line)
            valid = (isinstance(row, dict) and set(row) == {'invalid', 'snapshot'} and
                     all(type(row[key]) is int and row[key] in (0, 1) for key in row))
        except (ValueError, TypeError):
            valid = False
        require(valid, 'unfinished task billing evidence is unreadable; refuse old-program rollback')
        require(row['invalid'] == 0, 'unfinished task has unknown or malformed private billing context; wait for verified settlement')
        require(row['snapshot'] == 0, 'unfinished task retains a carpool billing snapshot; wait for r15.18 settlement before rollback')


def rollback_billing_guard(container=None):
    """No override switch: uncertainty preserves the newer billing runtime."""
    schema = ops.schema_from_dsn(envmap(container if container is not None else ops.inspect(NAME))['SQL_DSN'])
    lines = sql_read("SELECT JSON_OBJECT('value',value) FROM options WHERE `key`='CarpoolBillingPolicy';", schema).splitlines()
    require(len(lines) <= 1, 'ambiguous billing policy; rollback requires maintenance review')
    raw = json.loads(lines[0])['value'] if lines else ''
    if raw:
        try:
            def unique_object(pairs):
                result = {}
                for key, value in pairs:
                    if key in result:
                        raise ValueError('duplicate policy field')
                    result[key] = value
                return result
            policy = json.loads(raw, object_pairs_hook=unique_object)
            issued = datetime.fromisoformat(policy['issued_at'].replace('Z', '+00:00'))
            groups, rules = policy['groups'], policy['model_multipliers']
            valid = (set(policy) == {'version', 'issued_at', 'groups', 'model_multipliers'} and
                     re.fullmatch(r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})', policy['issued_at']) is not None and
                     type(policy['version']) is int and policy['version'] == 1 and issued.tzinfo is not None and
                     isinstance(groups, list) and all(isinstance(g, str) and g for g in groups) and len(set(groups)) == len(groups) and
                     isinstance(rules, dict) and all(isinstance(k, str) and k and type(v) in (int, float) and math.isfinite(v) and 0.1 <= v <= 10 for k, v in rules.items()))
        except (ValueError, TypeError, KeyError, AttributeError):
            valid = False
        require(valid, 'invalid billing policy may retain an earlier active snapshot; refuse old-program rollback')
        require(not rules, 'nonempty billing multipliers require maintenance restoration before old-program rollback')
    cp = envmap(ops.inspect('carpool-backend'))
    cp_schema = ops.schema_from_dsn(cp['MYSQL_DSN'])
    columns = set(sql_read("SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() "
                           "AND TABLE_NAME='model_price_setting';", cp_schema).splitlines())
    if 'luna_migrated_at' in columns:
        require(sql_read('SELECT COUNT(*) FROM model_price_setting WHERE luna_migrated_at IS NOT NULL;', cp_schema).strip() == '0',
                'Luna migration must be restored before old-program rollback')
    if 'billing_operation_id' in columns:
        require(sql_read('SELECT COUNT(*) FROM model_price_setting WHERE billing_operation_id IS NOT NULL;', cp_schema).strip() == '0',
                'unfinished billing operation prevents rollback')
    exists = sql_read("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='model_price_multiplier';", cp_schema).strip()
    if exists == '1':
        require(sql_read('SELECT COUNT(*) FROM model_price_multiplier WHERE multiplier <> 1;', cp_schema).strip() == '0',
                'CP still owns multiplier rules; stop/reconcile the writer before old-program rollback')
    unfinished_task_guard(schema)


def check_manifest():
    require(ADDED is not None and CHANGED is not None, 'reviewed source delta has not been pinned')
    record = load('build-manifest.json')
    require(record.get('version') == VERSION and record.get('baseline_image') == OLD and
            record.get('baseline_binary_sha256') == OLD_BINARY and record.get('baseline_container_id') == OLD_ID,
            'candidate manifest does not match the current r15.17 baseline')
    binary = sha_file(ROOT / 'new-api')
    require(record.get('binary_sha256') == binary, 'candidate binary checksum mismatch')
    previous = json.loads((PREVIOUS / 'build-manifest.json').read_text())
    require(previous.get('version') == OLD_VERSION and previous.get('binary_sha256') == OLD_BINARY,
            'r15.17 source archive is not tied to the running executable')
    old, new = record.get('baseline_source_files'), record.get('source_files')
    require(isinstance(old, dict) and isinstance(new, dict) and len(old) >= 500 and old == previous.get('source_files'),
            'source baseline must exactly match the deployed r15.17 manifest')
    for files in (old, new):
        for name, digest in files.items():
            relative = pathlib.PurePosixPath(name)
            require(not relative.is_absolute() and '..' not in relative.parts and
                    (name.endswith('.go') or name in ('go.mod', 'go.sum')) and
                    isinstance(digest, str) and re.fullmatch('[0-9a-f]{64}', digest), 'invalid Go source manifest entry')
    require(not set(old) - set(new) and set(new) - set(old) == ADDED, 'unapproved source addition/removal')
    require({name for name in old if old[name] != new[name]} == CHANGED, 'unapproved existing source changes')
    require(all(name in old and old[name] == new[name] for name in ('go.mod', 'go.sum')), 'dependency definitions changed')
    dependencies = record.get('baseline_dependencies')
    require(isinstance(dependencies, (dict, list)) and len(dependencies) == EXPECTED_DEPENDENCIES and
            dependencies == record.get('dependencies'), 'complete build-info dependency lists changed or are missing')
    if isinstance(dependencies, list):
        require(len({canonical(item) for item in dependencies}) == len(dependencies), 'duplicate dependency records')
    previous_assets = json.loads((PREVIOUS / 'frontend-manifest.json').read_text())['files']
    assets = load('frontend-manifest.json')['files']
    require(assets == previous_assets and len(assets) == 956 and
            all('web/' + theme + '/dist/index.html' in assets for theme in ('default', 'classic')),
            'both embedded frontend themes must match all 956 r15.17 assets')
    return binary, assets


def preflight():
    current = ops.inspect(NAME)
    require(current['Id'] == OLD_ID and current['Image'] == OLD and current['State']['Running'] and
            current['State']['StartedAt'] == OLD_STARTED, 'r15.17 running container identity/start changed')
    require(ops.run(['docker', 'exec', current['Id'], 'sha256sum', '/proc/1/exe']).decode().split()[0] == OLD_BINARY,
            'running r15.17 executable changed')
    status, body = ops.api(URL, 'GET', '/api/status')
    require(status == 200 and body.get('data', {}).get('version') == OLD_VERSION, 'running r15.17 version changed')
    require(envmap(current).get(FLAG, '').strip().lower() == 'true', 'existing aggregate flag must remain enabled')
    require(all(m['Type'] == 'bind' for m in current['Mounts']), 'non-bind mount requires separate review')
    require(ops.maybe_inspect(CANDIDATE) is None and ops.maybe_inspect(BACKUP) is None, 'release container name exists')
    require(ops.api(CP_HEALTH, 'GET', '/health')[0] == 200, 'CP health failed before release')
    rollback_billing_guard(current)
    binary, assets = check_manifest()
    print(json.dumps({'preflight': True, 'baseline_version': OLD_VERSION, 'baseline_container_id': current['Id'],
                      'candidate_binary_sha256': binary, 'embedded_files': len(assets),
                      'aggregate_enabled': True, 'billing_policy_written': False}), flush=True)
    return current, binary, assets


def assert_invariants():
    require(option_snapshot()['stable'] == load('options-before.json')['stable'], 'production options changed outside the dynamic concurrency policy')
    require(schema_digests() == load('schema-before.json'), 'new-api schema/index definitions changed')
    require(running_others() == load('other-containers.json'), 'other service container/configuration changed')
    pricing = pricing_snapshot()
    before = load('pricing-before.json')
    require(pricing['semantic'] == before['semantic'],
            'public pricing content or version marker changed')
    require(hashlib.sha256(read('/')).hexdigest() == load('index-before.json')['sha256'], 'served frontend HTML changed')
    return pricing


def assert_image_base(built, original):
    require(original.get('Id') == OLD, 'image inspection returned an unexpected base identity')
    old_layers = original.get('RootFS', {}).get('Layers')
    layers = built.get('RootFS', {}).get('Layers')
    require(isinstance(old_layers, list) and isinstance(layers, list) and old_layers and
            layers[:len(old_layers)] == old_layers and len(layers) == len(old_layers) + 1,
            'candidate image must add only the binary COPY layer over the exact r15.17 filesystem')


def prepare():
    require(not any((ROOT / name).exists() for name in ('production-before.json', 'deployment-state.json', 'build-checks.json')), 'release already prepared; preserve recovery material')
    current, binary, assets = preflight()
    _, recovered = recover(ROOT / 'new-api')
    require({name: hashlib.sha256(content).hexdigest() for name, content in recovered.items()} == assets,
            'candidate embedded resources differ from either production theme')
    save('production-before.json', {'containers': {NAME: current}})
    save('other-containers.json', running_others())
    save('options-before.json', option_snapshot())
    save('schema-before.json', schema_digests())
    save('pricing-before.json', pricing_snapshot())
    save('index-before.json', {'sha256': hashlib.sha256(read('/')).hexdigest()})
    # The root-only release directory protects the server artifact; the image
    # must retain an executable binary for the unchanged configured runtime user.
    (ROOT / 'new-api').chmod(0o755)
    ops.run(['docker', 'tag', OLD, BASE_TAG])
    require(json.loads(ops.run(['docker', 'image', 'inspect', BASE_TAG]))[0]['Id'] == OLD, 'base tag no longer points to pinned r15.17 image')
    (ROOT / 'Dockerfile').write_text('FROM ' + BASE_TAG + '\nCOPY new-api /new-api\nLABEL carpool.release="' + RELEASE + '" carpool.binary.sha256="' + binary + '"\n')
    (ROOT / '.dockerignore').write_text('*\n!Dockerfile\n!new-api\n')
    ops.run(['docker', 'build', '--network=none', '--pull=false', '-t', IMAGE, str(ROOT)])
    built = json.loads(ops.run(['docker', 'image', 'inspect', IMAGE]))[0]
    original = json.loads(ops.run(['docker', 'image', 'inspect', OLD]))[0]
    assert_image_base(built, original)
    image = built['Id']
    save('image.json', {'id': image, 'tag': IMAGE, 'binary': binary})
    save('build-checks.json', {'passed': True, 'image': image, 'binary': binary, 'embedded_files': len(assets),
         'build_manifest_sha256': sha_file(ROOT / 'build-manifest.json'), 'frontend_manifest_sha256': sha_file(ROOT / 'frontend-manifest.json'),
         'prepared_at': stamp(), 'stage_executed': False})
    assert_invariants()
    print('Prepared r15.18; production unchanged; source/dependency and both 956-file frontend manifests verified', flush=True)


def verify_live():
    record = state()
    require(record.get('new_id') and not record.get('rolled_back'), 'no active release-owned candidate')
    image, before = load('image.json'), load('production-before.json')['containers'][NAME]
    current = ops.inspect(NAME)
    require(current['Id'] == record['new_id'] and current['Image'] == image['id'], 'live container is not this release candidate')
    assert_config(current, before, new=True)
    binary, assets = check_manifest()
    require(binary == image['binary'] and ops.run(['docker', 'exec', current['Id'], 'sha256sum', '/proc/1/exe']).decode().split()[0] == binary,
            'live candidate executable checksum mismatch')
    status, body = ops.api(URL, 'GET', '/api/status')
    require(status == 200 and body.get('data', {}).get('version') == VERSION and current['State']['Running'], 'candidate health/version failed')
    require(ops.api(CP_HEALTH, 'GET', '/health')[0] == 200, 'CP health failed')
    auth = envmap(ops.inspect('carpool-backend'))
    status, body = ops.api(URL, 'GET', '/api/log/?type=2&token_ids=1&page_size=1',
                           token=auth['NEWAPI_ADMIN_TOKEN'], user_id=auth['NEWAPI_ADMIN_USER_ID'])
    require(status == 200 and body.get('data', {}).get('token_filter_applied') is True, 'existing token-scoped log API changed')
    empty = {'version': 1, 'tokens': [], 'from_unix': None, 'to_unix': None, 'after_log_id': 0, 'through_log_id': 0}
    require(ops.api(URL, 'POST', '/api/log/usage/aggregate', empty)[0] == 401, 'anonymous aggregate access was not denied')
    status, body = ops.api(URL, 'POST', '/api/log/usage/aggregate', empty,
                           token=auth['NEWAPI_ADMIN_TOKEN'], user_id=auth['NEWAPI_ADMIN_USER_ID'])
    data = body.get('data', {})
    require(status == 200 and body.get('success') is True and data.get('version') == 1 and data.get('complete') is True and
            data.get('items') == [] and data.get('totals') == {'requests': 0, 'input_tokens': 0, 'output_tokens': 0, 'cached_tokens': 0, 'amount_micro': 0},
            'existing enabled empty aggregate contract changed')
    paths = sorted(p for p in assets if p.startswith('web/classic/dist/assets/codex-routes-20261005/'))[:6]
    legacy = 'web/classic/dist/assets/index-D4VkjDT5.js'
    if legacy in assets:
        paths.append(legacy)
    for path in paths:
        require(hashlib.sha256(read(path[len('web/classic/dist'):])).hexdigest() == assets[path], 'served current/legacy frontend asset changed')
    pricing = assert_invariants()
    save('final-verification.json', {'passed': True, 'verified_at': stamp(), 'version': VERSION, 'container_id': current['Id'],
         'image': current['Image'], 'binary_sha256': binary, 'embedded_files': len(assets), 'static_probes': len(paths) + 1,
         'pricing_raw_sha256': pricing['raw_sha256'], 'pricing_semantic_sha256': pricing['semantic_sha256'],
         'pricing_row_version': pricing['row_pricing_version'], 'pricing_content_equal': True,
         'pricing_row_marker_count': pricing['row_pricing_marker_count'],
         'options_unchanged_except_concurrency_policy': True, 'schema_unchanged': True, 'other_containers_unchanged': True,
         'concurrency_policy_sha256': option_snapshot()['concurrency_policy_sha256'], 'aggregate_enabled': True,
         'old_log_api': True, 'policy_written': False, 'luna_migrated': False, 'model_calls': False, 'browser_test': False})
    print('Verified r15.18 software-only cutover; pricing/assets/options unchanged; no billing policy or Luna migration performed', flush=True)


def cutover():
    require(not state(), 'existing cutover state requires explicit verify/rollback')
    before = load('production-before.json')['containers'][NAME]
    current, binary, _ = preflight()
    require(current['Id'] == before['Id'], 'baseline container changed after prepare')
    assert_config(current, before)
    image, checked = load('image.json'), load('build-checks.json')
    require(checked.get('passed') is True and checked['image'] == image['id'] and checked['binary'] == image['binary'] == binary and
            checked['build_manifest_sha256'] == sha_file(ROOT / 'build-manifest.json') and
            checked['frontend_manifest_sha256'] == sha_file(ROOT / 'frontend-manifest.json'), 'prepared candidate inputs changed')
    assert_invariants()
    record = {'old_id': before['Id'], 'new_id': None, 'expected_image': image['id'], 'candidate_name': CANDIDATE,
              'backup_name': BACKUP, 'started_at': stamp()}
    save('deployment-state.json', record)
    try:
        config = copy.deepcopy(before['Config'])
        config['Image'] = image['id']
        config['Labels'] = dict(config.get('Labels') or {})
        config['Labels'].update({LABEL: RELEASE, 'carpool.binary.sha256': binary,
                                'carpool.rollout.old_id': before['Id'], 'carpool.rollout.service': NAME})
        config['HostConfig'] = copy.deepcopy(before['HostConfig'])
        config['NetworkingConfig'] = {'EndpointsConfig': endpoints(before)}
        cid = ops.docker('POST', '/containers/create?name=' + CANDIDATE, config)['Id']
        record['new_id'] = cid
        save('deployment-state.json', record)
        candidate = ops.inspect(cid)
        require(candidate['Image'] == image['id'] and not candidate['State']['Running'], 'candidate image/state mismatch')
        assert_config(candidate, before, new=True)
        rollback_billing_guard()
        require(ops.inspect(NAME)['Id'] == before['Id'], 'live container moved immediately before cutover')
        ops.docker('POST', '/containers/' + before['Id'] + '/stop?t=30')
        ops.docker('POST', '/containers/' + before['Id'] + '/rename?name=' + BACKUP)
        for network in before['NetworkSettings']['Networks']:
            ops.docker('POST', '/networks/' + network + '/disconnect', {'Container': before['Id'], 'Force': True})
        ops.docker('POST', '/containers/' + cid + '/rename?name=' + NAME)
        ops.docker('POST', '/containers/' + cid + '/start')
        ops.wait_http(URL, '/api/status')
        verify_live()
        record['healthy'], record['finished_at'] = True, stamp()
        save('deployment-state.json', record)
    except Exception:
        # Only this invocation's cutover may attempt automatic recovery. The
        # standalone verify command is read-only and must never revert a later
        # deployment, CP rollout or operator's policy change.
        try:
            rollback()
        except Exception:
            save('recovery-required.json', {'at': stamp(), 'automatic_rollback_completed': False,
                                           'action': 'preserve containers; inspect billing state and ownership before recovery'})
            raise GuardError('cutover failed and guarded rollback was refused or failed; preserve recovery material')
        raise


def rollback():
    record = state()
    before = load('production-before.json')['containers'][NAME]
    image = load('image.json')
    require(record.get('old_id') == before['Id'] == OLD_ID and before['Image'] == OLD and
            record.get('expected_image') == image['id'] and record.get('candidate_name') == CANDIDATE and record.get('backup_name') == BACKUP,
            'rollback baseline ownership mismatch')
    old = ops.inspect(OLD_ID)
    require(old['Image'] == OLD and old['Name'] in ('/' + NAME, '/' + BACKUP), 'original recovery container identity changed')
    assert_config(old, before, network=False)
    candidates = {}
    for identity in (record.get('new_id'), CANDIDATE, NAME):
        if not identity:
            continue
        current = ops.maybe_inspect(identity)
        if not current or current['Id'] == OLD_ID:
            continue
        labels = current['Config'].get('Labels') or {}
        require(current['Image'] == image['id'] and labels.get(LABEL) == RELEASE and
                labels.get('carpool.rollout.old_id') == OLD_ID and labels.get('carpool.rollout.service') == NAME,
                'refusing to remove an unrelated or later same-image container')
        if record.get('new_id'):
            require(current['Id'] == record['new_id'], 'rollback candidate ID mismatch')
        else:
            require(current['Name'] == '/' + CANDIDATE, 'unrecorded create must retain exact candidate name')
            record['new_id'] = current['Id']
            save('deployment-state.json', record)
        candidates[current['Id']] = current
    # If cutover failed in the rename gap, inspect the owned container by ID for
    # its DSN without starting it; no HTTP policy write is needed for this guard.
    rollback_billing_guard(old)
    for cid in candidates:
        ops.docker('DELETE', '/containers/' + cid + '?force=true')
    if old['Name'] != '/' + NAME:
        require(ops.maybe_inspect(NAME) is None, 'live name was claimed during rollback')
        ops.docker('POST', '/containers/' + OLD_ID + '/rename?name=' + NAME)
    for network, endpoint in endpoints(before).items():
        if network not in old['NetworkSettings']['Networks']:
            ops.docker('POST', '/networks/' + network + '/connect', {'Container': OLD_ID, 'EndpointConfig': endpoint})
    if not old['State']['Running']:
        ops.docker('POST', '/containers/' + OLD_ID + '/start')
    ops.wait_http(URL, '/api/status')
    current = ops.inspect(NAME)
    require(current['Id'] == OLD_ID and current['Image'] == OLD, 'old runtime was not restored')
    assert_config(current, before)
    require(ops.run(['docker', 'exec', OLD_ID, 'sha256sum', '/proc/1/exe']).decode().split()[0] == OLD_BINARY,
            'rollback executable mismatch')
    require(ops.api(URL, 'GET', '/api/status')[1].get('data', {}).get('version') == OLD_VERSION, 'rollback version mismatch')
    require(ops.api(CP_HEALTH, 'GET', '/health')[0] == 200, 'CP health failed after rollback')
    record['rolled_back'], record['healthy'] = True, False
    save('deployment-state.json', record)
    save('rollback-verification.json', {'passed': True, 'restored_at': stamp(), 'container_id': OLD_ID, 'version': OLD_VERSION,
         'binary_sha256': OLD_BINARY, 'database_restored': False, 'billing_guard_passed': True})
    print('Restored exact r15.17 container; database/options untouched; aggregate runtime flag preserved', flush=True)


def main():
    global ops, recover
    import fcntl
    os.umask(0o077)
    commands = {'preflight': preflight, 'prepare': prepare, 'cutover': cutover, 'verify': verify_live, 'rollback': rollback}
    require(len(sys.argv) == 2 and sys.argv[1] in commands, 'usage: preflight|prepare|cutover|verify|rollback')
    require(os.geteuid() == 0 and pathlib.Path('/var/run/docker.sock').exists(), 'execute only as the root release operator on serverB')
    require(not ROOT.is_symlink(), 'release directory is a symlink')
    ROOT.mkdir(parents=True, exist_ok=True, mode=0o700)
    ROOT.chmod(0o700)
    require(not LOCK.is_symlink(), 'release lock is a symlink')
    with LOCK.open('a') as lock:
        os.fchmod(lock.fileno(), 0o600)
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise GuardError('another new-api software release command is running')
        spec = importlib.util.spec_from_file_location('billing_release_transport', '/opt/carpool/releases/sensitive-message-20260922/deploy.py')
        base = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(base)
        ops, recover = base.ops, base.recover
        commands[sys.argv[1]]()


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(json.dumps({'failed': True, 'error_type': type(error).__name__,
                          'message': str(error) if isinstance(error, GuardError) else 'transport failed; inspect root-only release state',
                          'error_fingerprint': hashlib.sha256(str(error).encode()).hexdigest()[:16]}), file=sys.stderr)
        sys.exit(1)
