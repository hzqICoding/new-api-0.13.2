"""Two approved Codex setup entrances; preserve the r15.12 production baseline."""
import hashlib
import importlib.util
import pathlib
import sys

spec = importlib.util.spec_from_file_location('ui_release', '/opt/carpool/releases/newapi-codex-ui-20261005/deploy_ui.py')
ui = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ui)
release = ui.release
release.ROOT = release.base.ROOT = release.ops.ROOT = pathlib.Path('/opt/carpool/releases/newapi-codex-routes-20261005')
release.RELEASE = release.base.RELEASE = release.ops.RELEASE = 'newapi-codex-routes-20261005'
release.VERSION = release.base.VERSION = 'v0.13.2-carpool-r15.13-codex-routes-20261005'
release.IMAGE = release.base.IMAGE = 'hzqicoding/new-api:carpool-r15.13-codex-routes-20261005'
release.OLD = release.base.OLD = 'sha256:60de5cd305bb1114f5128059701693a629442401b7d915869d1f3837a3deb71a'
release.NS = 'codex-routes-20261005'
release.base.BINARY = hashlib.sha256((release.ROOT / 'new-api').read_bytes()).hexdigest()

if __name__ == '__main__':
    {'prepare': release.prepare, 'stage': release.stage, 'cutover': release.base.cutover,
     'verify': release.verify_live, 'rollback': lambda: release.ops.rollback('new-api')}[sys.argv[1]]()
