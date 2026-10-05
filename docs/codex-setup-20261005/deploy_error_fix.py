"""Reuse r15.10's verified release procedure with a pinned r15.10 baseline."""
import hashlib
import importlib.util
import pathlib
import sys

spec = importlib.util.spec_from_file_location('codex_release', '/opt/carpool/releases/newapi-codex-setup-20261005/deploy.py')
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)
release.ROOT = release.base.ROOT = release.ops.ROOT = pathlib.Path('/opt/carpool/releases/newapi-codex-error-fix-20261005')
release.RELEASE = release.base.RELEASE = release.ops.RELEASE = 'newapi-codex-error-fix-20261005'
release.VERSION = release.base.VERSION = 'v0.13.2-carpool-r15.11-codex-error-fix-20261005'
release.IMAGE = release.base.IMAGE = 'hzqicoding/new-api:carpool-r15.11-codex-error-fix-20261005'
release.OLD = release.base.OLD = 'sha256:71d2e997c6bf6c8bbd5983ddbf9041d01e298712afb15aa27688a448964551e7'
release.NS = 'codex-error-fix-20261005'
release.base.BINARY = hashlib.sha256((release.ROOT / 'new-api').read_bytes()).hexdigest()

if __name__ == '__main__':
    {'prepare': release.prepare, 'stage': release.stage, 'cutover': release.base.cutover,
     'verify': release.verify_live, 'rollback': lambda: release.ops.rollback('new-api')}[sys.argv[1]]()
