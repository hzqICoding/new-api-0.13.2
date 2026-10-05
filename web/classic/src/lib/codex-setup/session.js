// Event payloads contain token IDs only; never credentials.
export const OPEN_EVENT = 'newapi:codex-setup-open';
export const INVALIDATE_EVENT = 'newapi:codex-setup-invalidate';
export const normalizeTokenIds = (ids) => [
  ...new Set(
    (Array.isArray(ids) ? ids : []).filter(
      (id) => Number.isSafeInteger(id) && id > 0,
    ),
  ),
];
export function openCodexSetup(options) {
  window.dispatchEvent(
    new CustomEvent(OPEN_EVENT, {
      detail: {
        tokenIds: normalizeTokenIds(options.tokenIds),
        initialTokenId: options.initialTokenId,
        userIdentity: options.userIdentity,
      },
    }),
  );
}
export function invalidateCodexSetup(tokenIds) {
  window.dispatchEvent(
    new CustomEvent(INVALIDATE_EVENT, {
      detail: { tokenIds: normalizeTokenIds(tokenIds) },
    }),
  );
}

// Shared by the UI and focused tests: every close/change/timeout invalidates in-flight work.
export function createSetupEpoch() {
  let generation = 0;
  let controller = null;
  return {
    invalidate() {
      generation += 1;
      controller?.abort();
      controller = null;
    },
    begin() {
      this.invalidate();
      controller = new AbortController();
      return { generation, signal: controller.signal };
    },
    current(ticket) {
      return generation === ticket.generation && !ticket.signal.aborted;
    },
  };
}

// Production auth state can change before React props/polling catches up. Read
// the external session guard at every async boundary and immediately before save.
export function isSetupTicketCurrent(guard, ticket, active, isSessionCurrent) {
  if (
    !guard.current(ticket) ||
    !active.visible ||
    active.tokenId !== ticket.tokenId ||
    active.userIdentity !== ticket.userIdentity
  )
    return false;
  try {
    return !isSessionCurrent || isSessionCurrent() === true;
  } catch {
    return false;
  }
}
