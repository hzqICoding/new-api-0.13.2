"""Build from the verified production archive, preserving unrelated customizations."""
import hashlib
import json
import pathlib
import shutil
import sys
import tarfile

ROOT = pathlib.Path(__file__).resolve().parents[2]
WORK = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else 'E:/tmp/newapi-codex-setup-20261005')
BUILD = WORK / 'build'
NS = 'codex-setup-20261005'
OLD_NS = 'frontend-fix-20260925'
MAIN = 'index-luna-0216aa8f9328.js'


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


source = json.loads((WORK / 'source-manifest.json').read_text(encoding='utf-8'))
front = json.loads((WORK / 'frontend-manifest.json').read_text(encoding='utf-8'))['files']
for name, expected in source.items():
    assert digest(WORK / 'production-source' / name) == expected, name
for name, expected in front.items():
    assert digest(WORK / 'production-frontend' / name) == expected, name
BUILD.mkdir(exist_ok=True)
shutil.copytree(WORK / 'production-source', BUILD, dirs_exist_ok=True)
shutil.copytree(WORK / 'production-frontend', BUILD, dirs_exist_ok=True)

changed_source = ['controller/token.go', 'controller/token_codex_config.go', 'controller/token_codex_config_test.go', 'model/codex_setup.go', 'router/api-router.go']
for name in changed_source:
    shutil.copyfile(ROOT / name, BUILD / name)
# Read-only support assets omitted by the historical Go-source-only archive.
shutil.copytree(ROOT / 'i18n/locales', BUILD / 'i18n/locales', dirs_exist_ok=True)
shutil.copytree(ROOT / 'common/limiter/lua', BUILD / 'common/limiter/lua', dirs_exist_ok=True)

dist = BUILD / 'web/classic/dist'
new_assets = dist / 'assets' / NS
if new_assets.exists():
    resolved = new_assets.resolve()
    expected = (WORK / 'build' / 'web' / 'classic' / 'dist' / 'assets' / NS).resolve()
    assert resolved == expected and resolved.is_relative_to(WORK.resolve()) and NS != OLD_NS
    # Only this task's generated namespace; never an original production asset.
    shutil.rmtree(new_assets)
shutil.copytree(dist / 'assets' / OLD_NS, new_assets, dirs_exist_ok=True)
main_path = new_assets / MAIN
code = main_path.read_text(encoding='utf-8')
assert 'group_ratio_override' in code, 'Luna custom pricing lost in baseline'

patches = []
def replace_once(old, new, label):
    global code
    count = code.count(old)
    assert count == 1, f'{label}: expected one source anchor, got {count}'
    code = code.replace(old, new)
    patches.append(label)

replace_once('return a.jsxs(Qe,{wrap:!0,children:[a.jsxs(aY,{className:"overflow-hidden","aria-label":l("项目操作按钮组")',
             'return a.jsxs(Qe,{wrap:!0,children:[a.jsx(F,{size:"small",type:"primary",disabled:t.status!==1,onClick:()=>window.__newapiOpenCodexSetup([t.id]),children:l("Codex 快速配置")}),a.jsxs(aY,{className:"overflow-hidden","aria-label":l("项目操作按钮组")', 'token-row-entry')
replace_once('const _=parseInt(S.tokenCount,10)||1;let C=0;for(let k=0;k<_;k++)',
             'const _=parseInt(S.tokenCount,10)||1;let C=0,newapiCreatedIds=[];for(let k=0;k<_;k++)', 'create-batch-ids')
replace_once('const{success:E,message:$}=M.data;if(E)C++;else{L(t($));break}',
             'const{success:E,message:$}=M.data;if(E){C++;Number.isSafeInteger(M.data.data?.id)&&newapiCreatedIds.push(M.data.data.id)}else{L(t($));break}', 'create-exact-id')
replace_once('C>0&&(je(t("令牌创建成功，请在列表页面点击复制获取令牌！")),e.refresh(),e.handleClose())',
             'C>0&&(je(t("令牌创建成功，请在列表页面点击复制获取令牌！")),e.refresh(),e.handleClose(),newapiCreatedIds.length&&window.__newapiOpenCodexSetup(newapiCreatedIds))', 'create-wizard')
replace_once('if(oe){je(r("操作成功完成！"));let _e=ae.data.data,Ze=[...n];',
             'if(oe){window.dispatchEvent(new CustomEvent("newapi:codex-setup-invalidate",{detail:{tokenIds:[ve]}}));je(r("操作成功完成！"));let _e=ae.data.data,Ze=[...n];', 'token-state-invalidation')
replace_once('D?(je(t("令牌更新成功！")),e.refresh(),e.handleClose())',
             'D?(window.dispatchEvent(new CustomEvent("newapi:codex-setup-invalidate",{detail:{tokenIds:[Number(e.editingToken.id)]}})),je(t("令牌更新成功！")),e.refresh(),e.handleClose())', 'token-edit-invalidation')
replace_once('if((ve=re==null?void 0:re.data)!=null&&ve.success){const ae=re.data.data||0;je(r("已删除 {{count}} 个令牌！",{count:ae}))',
             'if((ve=re==null?void 0:re.data)!=null&&ve.success){window.dispatchEvent(new CustomEvent("newapi:codex-setup-invalidate",{detail:{tokenIds:ye}}));const ae=re.data.data||0;je(r("已删除 {{count}} 个令牌！",{count:ae}))', 'batch-delete-invalidation')
main_path.write_text(code, encoding='utf-8', newline='\n')

# Relocate dependency-preload strings as well as HTML URLs. Relative imports
# continue to share one React/API/i18n graph inside the new namespace.
for file in new_assets.rglob('*'):
    if file.is_file() and file.suffix in ('.js', '.css'):
        text = file.read_text(encoding='utf-8')
        if 'assets/' + OLD_NS + '/' in text:
            file.write_text(text.replace('assets/' + OLD_NS + '/', 'assets/' + NS + '/'), encoding='utf-8', newline='\n')
addon_meta = json.loads((WORK / 'addon' / 'build-meta.json').read_text(encoding='utf-8'))
for name in addon_meta['outputs']:
    file = WORK / 'addon' / pathlib.Path(name).name
    if file.suffix == '.js': shutil.copyfile(file, new_assets / file.name)
bootstrap = '''(()=>{let loading;
window.__newapiOpenCodexSetup = function(ids) {
  const tokenIds = [...new Set((Array.isArray(ids) ? ids : []).filter(id => Number.isSafeInteger(id) && id > 0))];
  if (!tokenIds.length) return;
  loading ||= import('./codex-setup-entry.js');
  loading.then(module => module.open(tokenIds)).catch(() => {
    loading = null;
    window.alert('配置模块加载失败，请刷新页面重试。');
  });
};
})();
'''
(new_assets / 'codex-bootstrap.js').write_text(bootstrap, encoding='utf-8')
index = dist / 'index.html'
html = index.read_text(encoding='utf-8').replace('/assets/' + OLD_NS + '/', '/assets/' + NS + '/')
html = html.replace('  </head>', f'    <script src="/assets/{NS}/codex-bootstrap.js"></script>\n  </head>')
assert f'/assets/{NS}/{MAIN}' in html and html.count('codex-bootstrap.js') == 1
index.write_text(html, encoding='utf-8', newline='\n')

all_front = {p.relative_to(BUILD).as_posix(): digest(p) for theme in ['default', 'classic'] for p in (BUILD / 'web' / theme / 'dist').rglob('*') if p.is_file()}
old_changed = [name for name, sha in front.items() if all_front.get(name) != sha]
assert old_changed == ['web/classic/dist/index.html'], old_changed
existing_source_changes = [name for name, sha in source.items() if digest(BUILD / name) != sha]
assert set(existing_source_changes) == {'controller/token.go', 'router/api-router.go'}, existing_source_changes
archive = WORK / 'newapi-codex-source.tar.gz'
new_source = dict(source)
for name in changed_source:
    new_source[name] = digest(BUILD / name)
with tarfile.open(archive, 'w:gz') as tar:
    for name in sorted(new_source): tar.add(BUILD / name, arcname=name)
(WORK / 'new-source-manifest.json').write_text(json.dumps(new_source, indent=2), encoding='utf-8')
(WORK / 'new-frontend-manifest.json').write_text(json.dumps({'files': all_front}, indent=2), encoding='utf-8')
evidence = {'preserved_old_assets': len(front)-1, 'old_files_changed': old_changed,
            'existing_go_changed': existing_source_changes, 'source_files': len(new_source),
            'frontend_files': len(all_front), 'patches': patches}
(WORK / 'build-evidence.json').write_text(json.dumps(evidence, indent=2), encoding='utf-8')
print(json.dumps(evidence, indent=2))
