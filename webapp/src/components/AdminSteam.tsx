import React from 'react';

import {fetchAdminSteam} from '../client';
import {PRODUCT_NAME} from '../name';
import type {AdminSteam as Status} from '../types';

import {AccountCardLoading, AccountCardStatus, AccountPeople, accountsHead, ago, plural, useAccountCard, useCardIds} from './AdminAccountParts';
import {AdminCard, AdminHead} from './adminParts';
import type {AdminCardProps} from './adminParts';

// System Console > Plugins > Matty > Steam > Steam connections, the SteamSetup
// setting (design/3.4.0.md 3.8): AdminTwitch's look and idiom. Where Steam
// sends people back to (nothing to register), whether the Web API key is
// set and works, when Steam was last asked and how many people it answered
// for, who has connected, who is blocked, and a way to delete everything
// the plugin holds from Steam. The shared half is AdminAccountParts.tsx.
//
// Nobody's status: the card works for a locked admin, and locked people see
// no Steam status. So the last check counts the people
// Steam answered for, never how many are online, which with one or two
// people connected would be their status.

const OFF_LINE = 'Show people’s Steam status is off: nobody can connect and nothing is asked of Steam. Connected people below can still be disconnected.';
const NO_KEY_LINE = 'Nothing is asked of Steam until the Web API key is set. Connected people below can still be disconnected.';

// When Steam was last asked, and how many it answered for: of the connected
// people, or of the active ones when somebody connected is deactivated,
// since Steam is never asked about them.
function lastCheckOf(status: Status, now: number): {text: string; error: boolean} {
    if (!status.enabled) {
        return {text: OFF_LINE, error: false};
    }
    if (!status.keySet) {
        return {text: NO_KEY_LINE, error: false};
    }
    if (status.problem && status.problem !== status.keyProblem) {
        return {text: status.problem, error: true};
    }
    if (status.keyProblem) {
        return {text: status.lastTry ? `Asked Steam ${ago(status.lastTry, now)}; it refused the key.` : 'Steam refused the key.', error: true};
    }

    const active = status.people.filter((person) => person.active).length;
    if (!status.lastTry || status.asked === 0) {
        if (status.people.length === 0) {
            return {text: 'Nothing to ask Steam yet: nobody has connected.', error: false};
        }

        return {text: active === 0 ? 'Nobody connected is active in Mattermost, so nothing is asked of Steam.' : 'Not asked yet. Steam is asked once a minute.', error: false};
    }

    const kind = active < status.people.length ? 'active' : 'connected';
    const all = status.answered === status.asked;
    let whom = `${status.answered.toLocaleString()} of the ${plural(status.asked, `${kind} person`, `${kind} people`)}`;
    if (all && status.asked === 1) {
        whom = `the one ${kind} person`;
    } else if (all && status.asked === 2) {
        whom = `both ${kind} people`;
    } else if (all) {
        whom = `all ${plural(status.asked, `${kind} person`, `${kind} people`)}`;
    }

    return {text: `Asked Steam ${ago(status.lastTry, now)}; it answered for ${whom}.`, error: false};
}

export const AdminSteam: React.FC<AdminCardProps> = ({id, label, disabled}) => {
    const card = useAccountCard<Status>(fetchAdminSteam);
    const ids = useCardIds('steam');

    const status = card.status;
    if (status === null) {
        return (
            <AccountCardLoading
                id={id}
                label={label}
                card={card}
                service='steam'
            />
        );
    }

    const lastCheck = lastCheckOf(status, card.now);

    return (
        <AdminCard
            id={id}
            label={label}
            focus={card.focus.focus}
        >
            <AdminHead instant={true}>{accountsHead('steam', status.people.length)}</AdminHead>

            {/* For reference only: OpenID 2.0 sends the return address with
                each sign-in, so there is nothing to copy anywhere. */}
            <h4 className='vntv-admin-sub'>{'Steam sends people back to'}</h4>
            {status.callbackProblem ? (
                <p className='vntv-admin-line vntv-notice-error'>{status.callbackProblem}</p>
            ) : (
                <>
                    <div className='vntv-access-entry vntv-twitch-callback'>
                        <label
                            className='vntv-sr-only'
                            htmlFor={ids.callback}
                        >{'Steam sends people back to'}</label>
                        <input
                            id={ids.callback}
                            className='vntv-input vntv-twitch-callback-url'
                            type='text'
                            readOnly={true}
                            spellCheck={false}
                            value={status.callbackUrl}
                            onFocus={(event) => event.currentTarget.select()}
                        />
                    </div>
                    <p className='vntv-admin-line vntv-admin-quiet'>
                        {`Nothing to register: Steam needs no redirect address. Steam’s sign-in page names this server as ${status.realm}.`}
                    </p>
                </>
            )}

            <h4 className='vntv-admin-sub'>{'Web API key'}</h4>
            <p className='vntv-admin-line'>
                {status.keySet ? 'A Steam Web API key is set.' : 'Fill in the Steam Web API key above first.'}
            </p>
            {status.keyProblem ? <p className='vntv-admin-line vntv-notice-error'>{status.keyProblem}</p> : null}

            <h4 className='vntv-admin-sub'>{'Last check'}</h4>
            <p className={lastCheck.error ? 'vntv-admin-line vntv-notice-error' : 'vntv-admin-line'}>{lastCheck.text}</p>
            {status.enabled && status.keySet ? (
                <p className='vntv-admin-line vntv-admin-quiet'>{`Steam calls today: ${status.callsToday.toLocaleString()}.`}</p>
            ) : null}

            <AccountPeople
                service='steam'
                people={status.people}
                blocked={status.blocked}
                quarantined={status.quarantined}
                card={card}
                disabled={disabled}
                deleteAllId={ids.deleteAll}
            />

            <p className='vntv-admin-line vntv-admin-quiet'>{`${PRODUCT_NAME} is not made, endorsed or supported by Valve or Steam.`}</p>

            <AccountCardStatus
                card={card}
                service='steam'
            />
        </AdminCard>
    );
};
