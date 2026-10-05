"""Publish scoped wizard styles and preserve every previous production asset URL."""
import hashlib
import json
import pathlib
import shutil
import sys
import tarfile

PREVIOUS = pathlib.Path('E:/tmp/newapi-codex-error-fix-20261005')
WORK = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else 'E:/tmp/newapi-codex-ui-20261005')
BUILD = WORK / 'build'
OLD_NS = 'codex-error-fix-20261005'
NS = 'codex-ui-20261005'
if len(sys.argv) > 2:
    assert len(sys.argv) == 5, 'Arguments: output previous old_namespace new_namespace'
    PREVIOUS = pathlib.Path(sys.argv[2])
    OLD_NS, NS = sys.argv[3:5]
    assert all(value and all(c.islower() or c.isdigit() or c == '-' for c in value) for value in [OLD_NS, NS])
    assert OLD_NS != NS


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


assert WORK.resolve() != PREVIOUS.resolve()
source = json.loads((PREVIOUS / 'source-manifest.json').read_text(encoding='utf-8'))
front = json.loads((PREVIOUS / 'frontend-manifest.json').read_text(encoding='utf-8'))['files']
for name, expected in {**source, **front}.items():
    src = PREVIOUS / 'build' / name
    assert digest(src) == expected, f'Baseline mismatch: {name}'
    dest = BUILD / name
    dest.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(src, dest)
for support in ['i18n/locales', 'common/limiter/lua']:
    shutil.copytree(PREVIOUS / 'build' / support, BUILD / support, dirs_exist_ok=True)

dist = BUILD / 'web/classic/dist'
assets = dist / 'assets' / NS
assert not assets.exists(), 'Use a fresh output directory for a reproducible UI build'
shutil.copytree(dist / 'assets' / OLD_NS, assets)
for file in assets.iterdir():
    if file.name.startswith('codex-setup-') and file.suffix in ('.js', '.css'):
        assert file.resolve().parent == assets.resolve()
        file.unlink()  # Only the new namespace's copied addon will be replaced.
    elif file.suffix in ('.js', '.css'):
        data = file.read_text(encoding='utf-8')
        file.write_text(data.replace('assets/' + OLD_NS + '/', 'assets/' + NS + '/'), encoding='utf-8', newline='\n')
meta = json.loads((WORK / 'addon/build-meta.json').read_text(encoding='utf-8'))
for name in meta['outputs']:
    file = WORK / 'addon' / pathlib.Path(name).name
    assert file.suffix in ('.js', '.css')
    shutil.copyfile(file, assets / file.name)
css = assets / 'codex-setup-entry.css'
assert css.is_file() and '.newapi-codex-setup' in css.read_text(encoding='utf-8')
index = dist / 'index.html'
html = index.read_text(encoding='utf-8').replace('/assets/' + OLD_NS + '/', '/assets/' + NS + '/')
assert f'/assets/{NS}/index-luna-0216aa8f9328.js' in html
assert html.count('codex-bootstrap.js') == 1
if 'codex-setup-entry.css' not in html:
    html = html.replace('  </head>', f'    <link rel="stylesheet" href="/assets/{NS}/codex-setup-entry.css" />\n  </head>')
assert html.count('codex-setup-entry.css') == 1
index.write_text(html, encoding='utf-8', newline='\n')

all_front = {p.relative_to(BUILD).as_posix(): digest(p) for theme in ['default', 'classic'] for p in (BUILD / 'web' / theme / 'dist').rglob('*') if p.is_file()}
old_changes = [name for name, sha in front.items() if all_front.get(name) != sha]
assert old_changes == ['web/classic/dist/index.html'], old_changes
assert all(digest(BUILD / name) == sha for name, sha in source.items()), 'Backend source changed in a UI-only release'
with tarfile.open(WORK / 'newapi-codex-source.tar.gz', 'w:gz') as tar:
    for name in sorted(source):
        tar.add(BUILD / name, arcname=name)
with tarfile.open(WORK / 'frontend.tar.gz', 'w:gz') as tar:
    for name in sorted(all_front):
        tar.add(BUILD / name, arcname=name)
(WORK / 'source-manifest.json').write_text(json.dumps(source, indent=2), encoding='utf-8')
(WORK / 'frontend-manifest.json').write_text(json.dumps({'files': all_front}, indent=2), encoding='utf-8')
evidence = {'source_files': len(source), 'backend_source_unchanged': True,
            'frontend_files': len(all_front), 'preserved_old_assets': len(front) - 1,
            'old_assets_changed': old_changes, 'namespace': NS,
            'stylesheet': 'codex-setup-entry.css', 'css_sha256': digest(css)}
(WORK / 'build-evidence.json').write_text(json.dumps(evidence, indent=2), encoding='utf-8')
print(json.dumps(evidence, indent=2))
