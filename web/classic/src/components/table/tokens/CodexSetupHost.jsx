import React, { useCallback, useEffect, useState } from 'react';
import CodexSetupModal from './modals/CodexSetupModal';
import {
  OPEN_EVENT,
  normalizeTokenIds,
} from '../../../lib/codex-setup/session';

// An independent production entry can mount this Host without TokensPage.
export default function CodexSetupHost({ request, t, userIdentity }) {
  const [options, setOptions] = useState(null);
  const close = useCallback(() => setOptions(null), []);
  const isSessionCurrent = useCallback(() => {
    try {
      return !!userIdentity && JSON.parse(localStorage.getItem('user') || 'null')?.id === userIdentity;
    } catch { return false; }
  }, [userIdentity]);
  useEffect(() => {
    close();
  }, [userIdentity, close]);
  useEffect(() => {
    const open = (event) => {
      const detail = event.detail || {};
      if (
        detail.userIdentity !== undefined &&
        detail.userIdentity !== userIdentity
      )
        return;
      const tokenIds = normalizeTokenIds(detail.tokenIds);
      if (tokenIds.length)
        setOptions({ tokenIds, initialTokenId: detail.initialTokenId });
    };
    window.addEventListener(OPEN_EVENT, open);
    return () => window.removeEventListener(OPEN_EVENT, open);
  }, [userIdentity]);
  return (
    <CodexSetupModal
      visible={!!options}
      tokenIds={options?.tokenIds || []}
      initialTokenId={options?.initialTokenId}
      userIdentity={userIdentity}
      isSessionCurrent={isSessionCurrent}
      onClose={close}
      request={request}
      t={t}
    />
  );
}
