import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import assert from 'node:assert/strict';
const root = path.resolve(process.argv[2] || 'E:/tmp/newapi-codex-setup-20261005/build/web/classic/dist');
const code = fs.readFileSync(path.join(root, 'assets/codex-setup-20261005/index-luna-0216aa8f9328.js'), 'utf8');
const events = [];
const context = vm.createContext({
  a: { jsx: (type, props) => ({ type, props }), jsxs: (type, props) => ({ type, props }) },
  Qe: 'Space', F: 'Button', aY: 'Split', sr: 'Dropdown', Nz: 'Icon', et: {},
  localStorage: { getItem: () => '[]' }, L: () => {},
  window: { __newapiOpenCodexSetup: (ids) => events.push([...ids]) },
});
const start = code.indexOf('VNn=(e,t,r,n,i,o,s,l)=>');
const end = code.indexOf(']})},', start) + 4;
assert.ok(start > 0 && end > start);
const ops = vm.runInContext('(' + code.slice(start + 'VNn='.length, end) + ')', context);
const row = ops(null, { id: 41, status: 1 }, () => {}, () => {}, () => {}, () => {}, () => {}, (x) => x);
assert.equal(row.props.children[0].props.children, 'Codex 快速配置');
assert.equal(row.props.children[0].props.disabled, false);
row.props.children[0].props.onClick();
assert.deepEqual(events.pop(), [41]);
assert.equal(ops(null, { id: 41, status: 2 }, null, null, null, null, null, (x) => x).props.children[0].props.disabled, true);

const createStart = code.indexOf('const _=parseInt(S.tokenCount,10)||1;let C=0,newapiCreatedIds=[];');
const createEnd = code.indexOf(')}o(!1)', createStart) + 1;
assert.ok(createStart > 0 && createEnd > createStart);
const body = code.slice(createStart, createEnd);
async function createScenario(responses) {
  events.length = 0;
  context.pe = { post: async () => ({ data: responses.shift() }) };
  context.t = (x) => x;
  context.je = () => {};
  context.o = () => {};
  context.w = () => 'suffix';
  context.e = { refresh: () => {}, handleClose: () => {} };
  const run = vm.runInContext('(async S=>{' + body + '})', context);
  await run({ tokenCount: 2, name: 'same-name', unlimited_quota: true, expired_time: -1, model_limits: [] });
  return [...events];
}
assert.deepEqual(await createScenario([{ success: true, data: { id: 101, key: 'NEVER-PASS-THIS' } }, { success: true, data: { id: 102 } }]), [[101, 102]]);
assert.deepEqual(await createScenario([{ success: true, data: { id: 101 } }, { success: false, message: 'limit' }]), [[101]]);
assert.deepEqual(await createScenario([{ success: true }, { success: true }]), [], 'old server must not guess the new IDs');
assert.ok(code.includes('group_ratio_override'), 'Luna pricing override must survive');
assert.ok(code.includes('newapi:codex-setup-invalidate'), 'state/edit/delete invalidation missing');
console.log('PASS: production row action, disabled token, exact batch IDs, partial success, old-server compatibility and retained pricing');
