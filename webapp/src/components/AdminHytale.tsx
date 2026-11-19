import React from 'react';

import {fetchAdminHytale} from '../client';
import type {AdminHytale as Status} from '../types';

import {AccountCardLoading, AccountCardStatus, AccountPeople, accountsHead, useAccountCard, useCardIds} from './AdminAccountParts';
import {AdminCard, AdminHead, AdminPill} from './adminParts';
import type {AdminCardProps} from './adminParts';

// System Console > Plugins > Matty > Hytale > Hytale connections, the HytaleSetup
// setting (design/3.4.0.md 4.7): AdminTwitch's look and idiom. The exact
// Redirect URI to register with Hytale, with Copy; whether the application
// is set up and what Hytale last said of it; who has connected, who is
// blocked, and a way to delete everything the plugin holds from Hytale. The
// shared half is AdminAccountParts.tsx.

const OFF_LINE = 'Show people’s Hytale names is off: nobody can connect and no Hytale names show. Connected people below can still be disconnected.';

export const AdminHytale: React.FC<AdminCardProps> = ({id, label, disabled}) => {
    const card = useAccountCard<Status>(fetchAdminHytale);
    const ids = useCardIds('hytale');
    const urlRef = React.useRef<HTMLInputElement | null>(null);

    const status = card.status;
    if (status === null) {
        return (
            <AccountCardLoading
                id={id}
                label={label}
                card={card}
                service='hytale'
            />
        );
    }

    // Copy: the address exactly, as Hytale matches it, byte for byte. Where
    // the clipboard is refused, the address is selected for Ctrl+C instead.
    const copy = () => {
        const url = status.callbackUrl;
        if (url === '') {
            return;
        }

        const selectIt = () => {
            urlRef.current?.focus();
            urlRef.current?.select();
            card.setNote('Selected: press Ctrl+C (⌘C on a Mac) to copy it.');
        };

        if (!navigator.clipboard?.writeText) {
            selectIt();

            return;
        }

        navigator.clipboard.writeText(url).then(() => {
            card.setError('');
            card.setNote('Copied the Redirect URI.');
        }).catch(selectIt);
    };

    return (
        <AdminCard
            id={id}
            label={label}
            focus={card.focus.focus}
        >
            <AdminHead instant={true}>{accountsHead('hytale', status.people.length)}</AdminHead>

            <h4 className='vntv-admin-sub'>{'Redirect URI'}</h4>
            {status.callbackProblem ? (
                <p className='vntv-admin-line vntv-notice-error'>{status.callbackProblem}</p>
            ) : (
                <>
                    <div className='vntv-access-entry vntv-twitch-callback'>
                        <label
                            className='vntv-sr-only'
                            htmlFor={ids.callback}
                        >{'Redirect URI'}</label>
                        <input
                            ref={urlRef}
                            id={ids.callback}
                            className='vntv-input vntv-twitch-callback-url'
                            type='text'
                            readOnly={true}
                            spellCheck={false}
                            value={status.callbackUrl}
                            onFocus={(event) => event.currentTarget.select()}
                        />
                        <AdminPill
                            tone='primary'
                            waiting={card.busy !== ''}
                            label='Copy the Redirect URI'
                            onPress={copy}
                        >{'Copy'}</AdminPill>
                    </div>
                    <p className='vntv-admin-line vntv-admin-quiet'>
                        {'Add this exactly under Redirect URIs in your Hytale application (accounts.hytale.com, Third-party developer apps). Ask for a confidential client with the scopes openid and hytale:profile.'}
                    </p>
                </>
            )}

            <h4 className='vntv-admin-sub'>{'Application'}</h4>
            <p className='vntv-admin-line'>
                {status.credentials ? 'Client ID and Client Secret are set.' : 'Fill in the Client ID and Client Secret above first.'}
            </p>

            {/* What Hytale said at the last exchange or callback: the
                credentials refused, or the scope not granted yet. */}
            {status.problem ? <p className='vntv-admin-line vntv-notice-error'>{status.problem}</p> : null}
            {status.enabled ? null : <p className='vntv-admin-line vntv-admin-quiet'>{OFF_LINE}</p>}

            <AccountPeople
                service='hytale'
                people={status.people}
                blocked={status.blocked}
                quarantined={status.quarantined}
                card={card}
                disabled={disabled}
                deleteAllId={ids.deleteAll}
            />

            <AccountCardStatus
                card={card}
                service='hytale'
            />
        </AdminCard>
    );
};
