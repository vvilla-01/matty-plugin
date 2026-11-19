import React from 'react';

import {getAccounts, loadAccountsIfOld, onAccounts} from './accounts';
import {accountsVisible} from './accountsLogic';
import type {AccountsView} from './types';

// The accounts store (accounts.ts) for a component: it re-renders whenever
// the store changes. Kept apart from the store so that check-away.mjs can
// load the store without React.
export function useAccounts(): AccountsView | null {
    const [, bump] = React.useState(0);

    React.useEffect(() => onAccounts(() => bump((value) => value + 1)), []);

    return getAccounts();
}

// For the panel and a profile popover: what may be shown to this reader,
// asked for again as they open if the last answer is a minute old, and
// nothing at all for a reader who may not watch, whatever the store still
// holds for a moment.
export function useAccountsFor(access: string): AccountsView | null {
    const view = useAccounts();
    const visible = accountsVisible(access);

    React.useEffect(() => {
        if (visible) {
            loadAccountsIfOld();
        }
    }, [visible]);

    return visible ? view : null;
}
