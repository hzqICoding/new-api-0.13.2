"""Build the error-message fix from the exact r15.10 release, retaining old URLs."""
import hashlib
import json
import pathlib
import shutil
import sys
import tarfile

ROOT = pathlib.Path(__file__).resolve().parents[2]
PREVIOUS = pathlib.Path('E:/tmp/newapi-codex-setup-20261005')
WORK = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else 'E:/tmp/newapi-codex-error-fix-20261005')
BUILD = WORK / 'build'
OLD_NS = 'codex-setup-20261005'
NS = 'codex-error-fix-20261005'


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


assert WORK.resolve() != PREVIOUS.resolve()
source = json.loads((PREVIOUS / 'new-source-manifest.json').read_text(encoding='utf-8'))
front = json.loads((PREVIOUS / 'new-frontend-manifest.json').read_text(encoding='utf-8'))['files']
for name, expected in {**source, **front}.items():
    src = PREVIOUS / 'build' / name
    assert digest(src) == expected, f'Baseline mismatch: {name}'
    dest = BUILD / name
    dest.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(src, dest)
for support in ['i18n/locales', 'common/limiter/lua']:
    shutil.copytree(PREVIOUS / 'build' / support, BUILD / support, dirs_exist_ok=True)
changed = ['controller/token_codex_config.go', 'controller/token_codex_config_test.go']
for name in changed:
    shutil.copyfile(ROOT / name, BUILD / name)

dist = BUILD / 'web/classic/dist'
new_assets = dist / 'assets' / NS
# Fresh namespace only: fail rather than silently mixing a prior build.
assert not new_assets.exists(), 'Use a fresh output directory for a reproducible fix'
shutil.copytree(dist / 'assets' / OLD_NS, new_assets)
for file in new_assets.iterdir():
    if file.name.startswith('codex-setup-') and file.suffix == '.js':
        # These copied addon files will be replaced with the new build outputs.
        assert file.resolve().parent == new_assets.resolve()
        file.unlink()
    elif file.suffix in ('.js', '.css'):
        data = file.read_text(encoding='utf-8')
        file.write_text(data.replace('assets/' + OLD_NS + '/', 'assets/' + NS + '/'), encoding='utf-8', newline='\n')
meta = json.loads((WORK / 'addon/build-meta.json').read_text(encoding='utf-8'))
for name in meta['outputs']:
    file = WORK / 'addon' / pathlib.Path(name).name
    assert file.suffix == '.js'
    shutil.copyfile(file, new_assets / file.name)
index = dist / 'index.html'
html = index.read_text(encoding='utf-8').replace('/assets/' + OLD_NS + '/', '/assets/' + NS + '/')
assert f'/assets/{NS}/index-luna-0216aa8f9328.js' in html
assert html.count('codex-bootstrap.js') == 1
index.write_text(html, encoding='utf-8', newline='\n')

all_front = {p.relative_to(BUILD).as_posix(): digest(p) for theme in ['default', 'classic'] for p in (BUILD / 'web' / theme / 'dist').rglob('*') if p.is_file()}
old_changes = [name for name, sha in front.items() if all_front.get(name) != sha]
assert old_changes == ['web/classic/dist/index.html'], old_changes
source_changes = [name for name, sha in source.items() if digest(BUILD / name) != sha]
assert set(source_changes) == set(changed), source_changes
new_source = {name: digest(BUILD / name) for name in source}
with tarfile.open(WORK / 'newapi-codex-source.tar.gz', 'w:gz') as tar:
    for name in sorted(new_source):
        tar.add(BUILD / name, arcname=name)
with tarfile.open(WORK / 'frontend.tar.gz', 'w:gz') as tar:
    for name in sorted(all_front):
        tar.add(BUILD / name, arcname=name)
(WORK / 'source-manifest.json').write_text(json.dumps(new_source, indent=2), encoding='utf-8')
(WORK / 'frontend-manifest.json').write_text(json.dumps({'files': all_front}, indent=2), encoding='utf-8')
evidence = {'source_files': len(new_source), 'source_changes': source_changes,
            'frontend_files': len(all_front), 'preserved_old_assets': len(front) - 1,
            'old_assets_changed': old_changes, 'namespace': NS}
(WORK / 'build-evidence.json').write_text(json.dumps(evidence, indent=2), encoding='utf-8')
print(json.dumps(evidence, indent=2))
