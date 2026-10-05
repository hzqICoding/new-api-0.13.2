import React, { useEffect, useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { API } from '@newapi/production-api';
import CodexSetupModal from '../../web/classic/src/components/table/tokens/modals/CodexSetupModal';
import { normalizeTokenIds } from '../../web/classic/src/lib/codex-setup/session';

// Uses the production React/API/i18n singletons. No duplicate app or auth cache.
function identity() {
  try {
    const user = JSON.parse(localStorage.getItem('user') || 'null');
    return user?.id ? `${user.id}:${user.role || ''}` : '';
  } catch { return ''; }
}

let root;
let sequence = 0;
function close() { root?.render(null); }

function Launch({ tokenIds, owner }) {
  const [currentOwner, setCurrentOwner] = useState(identity);
  useEffect(() => {
    const check = () => setCurrentOwner(identity());
    window.addEventListener('storage', check);
    // Same-tab login changes do not emit a storage event. Request guards below
    // also check synchronously before/after every response (no polling gap).
    const timer = setInterval(check, 500);
    return () => { window.removeEventListener('storage', check); clearInterval(timer); };
  }, []);
  const request = useMemo(() => Object.fromEntries(['get', 'post'].map((method) => [method, async (...args) => {
    if (!owner || identity() !== owner) throw new Error('登录状态已变化，请重新打开配置向导。');
    const response = await API[method](...args);
    if (identity() !== owner) throw new Error('登录状态已变化，请重新打开配置向导。');
    return response;
  }])), [owner]);
  if (!owner || currentOwner !== owner) return null;
  return <CodexSetupModal visible tokenIds={tokenIds} userIdentity={owner} request={request}
    isSessionCurrent={() => identity() === owner} onClose={close} />;
}

export function open(tokenIds) {
  const ids = normalizeTokenIds(tokenIds);
  if (!ids.length || !identity()) return;
  if (!root) {
    const container = document.createElement('div');
    container.id = 'newapi-codex-setup-root';
    document.body.appendChild(container);
    root = createRoot(container);
  }
  root.render(<Launch key={++sequence} tokenIds={ids} owner={identity()} />);
}
