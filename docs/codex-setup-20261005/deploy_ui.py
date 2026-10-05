"""UI-only release from the pinned r15.11 production baseline."""
import hashlib
import importlib.util
import pathlib
import sys

spec = importlib.util.spec_from_file_location('codex_release', '/opt/carpool/releases/newapi-codex-setup-20261005/deploy.py')
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)
release.ROOT = release.base.ROOT = release.ops.ROOT = pathlib.Path('/opt/carpool/releases/newapi-codex-ui-20261005')
release.RELEASE = release.base.RELEASE = release.ops.RELEASE = 'newapi-codex-ui-20261005'
release.VERSION = release.base.VERSION = 'v0.13.2-carpool-r15.12-codex-ui-20261005'
release.IMAGE = release.base.IMAGE = 'hzqicoding/new-api:carpool-r15.12-codex-ui-20261005'
release.OLD = release.base.OLD = 'sha256:57967c259db06950d9e9668177b889f93e63968d4185278d6e1e821993bd9815'
release.NS = 'codex-ui-20261005'
release.base.BINARY = hashlib.sha256((release.ROOT / 'new-api').read_bytes()).hexdigest()

original_frontend_check = release.frontend_check


def frontend_check(url):
    count = original_frontend_check(url)
    path = f'/assets/{release.NS}/codex-setup-entry.css'
    assert f'href="{path}"' in release.read(url + '/').decode(), 'Scoped wizard stylesheet missing from HTML'
    actual = hashlib.sha256(release.read(url + path)).hexdigest()
    expected = release.ops.load('frontend-manifest.json')['files']['web/classic/dist' + path]
    assert actual == expected, 'Scoped wizard stylesheet checksum mismatch'
    return count + 1


release.frontend_check = frontend_check
if __name__ == '__main__':
    {'prepare': release.prepare, 'stage': release.stage, 'cutover': release.base.cutover,
     'verify': release.verify_live, 'rollback': lambda: release.ops.rollback('new-api')}[sys.argv[1]]()
