import React from 'react';

import {adminBlockAccount, adminClearAccountBlocks, adminUnlinkAccount, deleteAllAccount} from '../client';
import {PRODUCT_NAME} from '../name';
import type {AccountService, AdminAccountPerson, AdminTwitchBlocked} from '../types';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardConfirm, useCardFocus} from './adminParts';
import type {CardConfirm} from './adminParts';
import {Avatar} from './Avatar';

// What System Console > Steam connections and Hytale connections share
// (AdminSteam.tsx, AdminHytale.tsx; design/3.4.0.md 3.8 and 4.7): reading
// the card and acting on it the way AdminTwitch does, and its second half,
// which is the same on both but for the service's name: who has connected,
// with Disconnect and Disconnect and block, who is blocked, a set-aside copy,
// and Delete all.
//
// Everything here works whatever the switch says, so a connection can always
// be taken away, and only through admin routes, so it works for an admin
// whose own panel is locked. That is also why nobody's Steam status is here,
// nor whether a profile is private: locked people see no status. Read when shown and again after each action, newest answer wins,
// never polled. Taking a connection away, blocking, clearing the block list
// and deleting everything each take a second press.

export function errorText(reason: unknown): string {
    return reason instanceof Error ? reason.message : String(reason);
}

export function plural(count: number, one: string, many: string): string {
    return `${count.toLocaleString()} ${count === 1 ? one : many}`;
}

export function day(at: number): string {
    if (!at) {
        return '';
    }

    try {
        return new Date(at).toLocaleDateString(undefined, {day: 'numeric', month: 'short', year: 'numeric'});
    } catch {
        return '';
    }
}

// "40 seconds ago", "3 minutes ago", "2 hours ago".
export function ago(at: number, now: number): string {
    const seconds = Math.max(0, Math.round((now - at) / 1000));
    if (seconds < 60) {
        return plural(seconds, 'second ago', 'seconds ago');
    }

    const minutes = Math.round(seconds / 60);
    if (minutes < 60) {
        return plural(minutes, 'minute ago', 'minutes ago');
    }

    return plural(Math.round(minutes / 60), 'hour ago', 'hours ago');
}

export const SERVICE_WORD: Record<AccountService, string> = {steam: 'Steam', hytale: 'Hytale'};

const DELETE_ALL_HINT: Record<AccountService, string> = {
    steam: `Disconnects everybody and deletes every Steam ID and name ${PRODUCT_NAME} holds, including any set-aside copy. The block list is kept.`,
    hytale: `Disconnects everybody and deletes every Hytale username and profile id ${PRODUCT_NAME} holds, including any set-aside copy. The block list is kept.`,
};

const DELETE_ALL_AFTER: Record<AccountService, string> = {
    steam: 'Like all of the plugin’s data, Steam connections stay in the database if the plugin is removed. Press Delete all Steam data first when removing the plugin for good, or when you stop using Steam’s Web API.',
    hytale: 'Like all of the plugin’s data, Hytale connections stay in the database if the plugin is removed. Press Delete all Hytale data first when removing the plugin for good.',
};

const SET_ASIDE: Record<AccountService, string> = {
    steam: 'A damaged copy of the Steam connections was set aside and may still hold Steam names. Delete all Steam data removes it.',
    hytale: 'A damaged copy of the Hytale connections was set aside and may still hold Hytale usernames. Delete all Hytale data removes it.',
};

// A person whose Mattermost account is deactivated or deleted: kept, so an
// admin can still disconnect them, but never asked about and never shown.
const INACTIVE_TITLE: Record<AccountService, string> = {
    steam: 'Deactivated or deleted in Mattermost: Steam is not asked about them, and nobody is shown their Steam name.',
    hytale: 'Deactivated or deleted in Mattermost: nobody is shown their Hytale username.',
};

// Ids from a counter (see TitleSearch). Not vntv-, which the style check
// reads as a class.
let nextCard = 0;

export function useCardIds(service: AccountService): {callback: string; deleteAll: string} {
    const [ids] = React.useState(() => {
        nextCard += 1;

        return {callback: `villanet-${service}-callback-${nextCard}`, deleteAll: `villanet-${service}-deleteall-${nextCard}`};
    });

    return ids;
}

export type AccountCard<T> = {
    status: T | null;
    loadError: string;
    busy: string;
    note: string;
    setNote: (note: string) => void;
    error: string;
    setError: (error: string) => void;
    now: number;
    focus: ReturnType<typeof useCardFocus>;
    confirm: CardConfirm;
    load: () => void;
    act: (what: string, work: () => Promise<unknown>, done: string) => void;
};

// Reading the card and acting on it, as AdminTwitch does: only the newest
// request's answer is used, since the read after an action can cross a
// Refresh, and an older answer landing last would bring back a connection
// just taken away; and every action is read again after, whether it worked
// or not, since a failure is often because it had already changed.
export function useAccountCard<T>(fetch: () => Promise<T>): AccountCard<T> {
    const [status, setStatus] = React.useState<T | null>(null);
    const [loadError, setLoadError] = React.useState('');
    const [busy, setBusy] = React.useState('');
    const [note, setNote] = React.useState('');
    const [error, setError] = React.useState('');
    const [now, setNow] = React.useState(() => Date.now());
    const focus = useCardFocus();
    const confirm = useCardConfirm(() => {
        setNote('');
        setError('');
    });

    const latest = React.useRef(0);
    const load = React.useCallback(() => {
        const ticket = ++latest.current;
        fetch().then((next) => {
            if (ticket === latest.current) {
                setStatus(next);
                setLoadError('');
                setNow(Date.now());
            }
        }).catch((reason: unknown) => {
            if (ticket === latest.current) {
                setLoadError(errorText(reason));
            }
        });
    }, [fetch]);

    React.useEffect(() => {
        load();

        return () => {
            latest.current++;
        };
    }, [load]);

    const act = (what: string, work: () => Promise<unknown>, done: string) => {
        setBusy(what);
        setNote('');
        setError('');
        confirm.disarm();

        work().then(() => {
            setNote(done);
        }).catch((reason: unknown) => {
            setError(errorText(reason));
        }).finally(() => {
            setBusy('');
            load();
        });
    };

    return {status, loadError, busy, note, setNote, error, setError, now, focus, confirm, load, act};
}

// The card before its first answer, or when that answer failed.
export const AccountCardLoading: React.FC<{id?: string; label?: string; card: AccountCard<unknown>; service: AccountService}> = ({id, label, card, service}) => (
    <AdminCard
        id={id}
        label={label}
        focus={card.focus.focus}
    >
        <AdminHead instant={true}>{card.loadError === '' ? 'Loading…' : `${SERVICE_WORD[service]} connections could not be loaded.`}</AdminHead>
        {card.loadError === '' ? null : (
            <div className='vntv-admin-actions'>
                <AdminPill onPress={card.load}>{'Refresh'}</AdminPill>
            </div>
        )}
        <AdminStatus
            ref={card.focus.statusRef}
            note=''
            error={card.loadError}
        />
    </AdminCard>
);

// The card's head: how many people have connected, or nobody.
export function accountsHead(service: AccountService, count: number): React.ReactNode {
    if (count === 0) {
        return `Nobody has connected a ${SERVICE_WORD[service]} account.`;
    }

    return (
        <>
            <b>{plural(count, 'person', 'people')}</b>
            {` connected to ${SERVICE_WORD[service]}`}
        </>
    );
}

// The card's status line, last.
export const AccountCardStatus: React.FC<{card: AccountCard<unknown>; service: AccountService}> = ({card, service}) => (
    <AdminStatus
        ref={card.focus.statusRef}
        note={card.confirm.hint || card.note}
        error={card.error || (card.loadError === '' ? '' : `${SERVICE_WORD[service]} connections could not be loaded: ${card.loadError}`)}
    />
);

// The name a card shows for somebody, in every place it names them: their
// Mattermost name, or, when Mattermost has none to give (the account is gone
// for good, or Mattermost did not answer just now), their user id, so no
// label, hint or note ever reads "Disconnect  from Steam".
function shownName(person: {userId: string; name: string}): string {
    return person.name || person.userId;
}

// The flag beside somebody whose account is not active: "account deleted"
// only when the server says Mattermost no longer has it; "deactivated" when
// Mattermost named them; and "deactivated or deleted" when it could not be
// asked, and the loop's last word is all there is.
function inactiveWord(person: AdminAccountPerson): string {
    if (person.deleted) {
        return 'account deleted';
    }

    return person.name === '' ? 'deactivated or deleted' : 'deactivated';
}

type PeopleProps = {
    service: AccountService;
    people: AdminAccountPerson[];
    blocked: AdminTwitchBlocked[];
    quarantined: boolean;
    card: AccountCard<unknown>;
    disabled?: boolean;
    deleteAllId: string;
};

// Connected people, Blocked, the set-aside copy and Delete all.
export const AccountPeople: React.FC<PeopleProps> = ({service, people, blocked, quarantined, card, disabled, deleteAllId}) => {
    const word = SERVICE_WORD[service];
    const waiting = card.busy !== '';
    const confirm = card.confirm;

    const disconnectOf = (person: AdminAccountPerson, block: boolean) => {
        const key = `${block ? 'block' : 'unlink'}:${person.userId}`;
        const who = shownName(person);
        const words = block ?
            `Click again to disconnect ${who} from ${word} and stop them connecting again.` :
            `Click again to disconnect ${who} from ${word}. They can connect again.`;

        return (event: {detail: number}) => {
            if (disabled || !confirm.press(key, words, event, person.userId)) {
                return;
            }

            card.act(key, () => adminUnlinkAccount(service, person.userId, block), block ?
                `Disconnected ${who} and blocked them from connecting ${word}.` :
                `Disconnected ${who} from ${word}.`);
        };
    };

    return (
        <>
            <h4 className='vntv-admin-sub'>{'Connected people'}</h4>
            {people.length === 0 ? (
                <p className='vntv-admin-line vntv-admin-quiet'>{`Nobody has connected a ${word} account.`}</p>
            ) : (
                <ul className='vntv-admin-list'>
                    {people.map((person) => {
                        const since = day(person.since);
                        const theirs = service === 'steam' ? person.steamName || '' : person.hytaleName || '';
                        const who = shownName(person);

                        return (
                            <li
                                key={person.userId}
                                className='vntv-admin-row'
                                data-align='top'
                            >
                                <span
                                    className='vntv-admin-thumb'
                                    data-shape='circle'
                                    data-size='small'
                                >
                                    <Avatar
                                        userId={person.userId}
                                        name={who}
                                        className='vntv-admin-thumb-image'
                                        decorative={true}
                                    />
                                </span>
                                <div className='vntv-admin-row-main'>
                                    <p className='vntv-admin-row-title'>
                                        <span className='vntv-admin-row-name'>{who}</span>
                                        {person.active ? null : (
                                            <span
                                                className='vntv-admin-flag'
                                                title={INACTIVE_TITLE[service]}
                                            >{inactiveWord(person)}</span>
                                        )}
                                    </p>

                                    {/* Their Steam name, a link to the profile the
                                        server built from their SteamID; or their
                                        Hytale username. Never a status. */}
                                    <p className='vntv-admin-row-meta'>
                                        {service === 'steam' && person.url && person.url.startsWith('https://steamcommunity.com/profiles/') ? (
                                            <a
                                                href={person.url}
                                                target='_blank'
                                                rel='noopener noreferrer'
                                            >{theirs}</a>
                                        ) : theirs}
                                        {service === 'hytale' ? ' (Hytale username)' : ''}
                                        {since ? ` · connected ${since}` : ''}
                                    </p>
                                    <div
                                        className='vntv-admin-actions'
                                        role='group'
                                        aria-label={`${who}’s ${word} connection`}
                                    >
                                        <AdminPill
                                            off={disabled}
                                            waiting={waiting}
                                            working={card.busy === `unlink:${person.userId}`}
                                            workingWords='Disconnecting…'
                                            confirms={true}
                                            armed={confirm.armed === `unlink:${person.userId}`}
                                            label={`Disconnect ${who} from ${word}`}
                                            onPress={disconnectOf(person, false)}
                                        >{'Disconnect'}</AdminPill>
                                        <AdminPill
                                            off={disabled}
                                            waiting={waiting}
                                            working={card.busy === `block:${person.userId}`}
                                            workingWords='Blocking…'
                                            confirms={true}
                                            armed={confirm.armed === `block:${person.userId}`}
                                            label={`Disconnect ${who} from ${word} and block them`}
                                            onPress={disconnectOf(person, true)}
                                        >{'Disconnect and block'}</AdminPill>
                                    </div>
                                </div>
                            </li>
                        );
                    })}
                </ul>
            )}

            {blocked.length > 0 ? (
                <>
                    <h4 className='vntv-admin-sub'>{'Blocked'}</h4>
                    <p className='vntv-admin-line vntv-admin-quiet'>{`Blocked people cannot connect a ${word} account.`}</p>
                    <ul className='vntv-admin-list'>
                        {blocked.map((person) => {
                            const who = shownName(person);

                            return (
                                <li
                                    key={person.userId}
                                    className='vntv-admin-row'
                                >
                                    <span className='vntv-admin-row-text'>
                                        {person.since ? `${who} · since ${day(person.since)}` : who}
                                    </span>
                                    <AdminPill
                                        off={disabled}
                                        waiting={waiting}
                                        working={card.busy === `unblock:${person.userId}`}
                                        workingWords='Unblocking…'
                                        label={`Unblock ${who}`}
                                        onPress={() => card.act(`unblock:${person.userId}`, () => adminBlockAccount(service, person.userId, false), `${who} can connect ${word} again.`)}
                                    >{'Unblock'}</AdminPill>
                                </li>
                            );
                        })}
                    </ul>
                    <div className='vntv-admin-actions'>
                        <AdminPill
                            off={disabled}
                            waiting={waiting}
                            working={card.busy === 'clearblocks'}
                            workingWords='Clearing…'
                            confirms={true}
                            armed={confirm.armed === 'clearblocks'}
                            onPress={(event) => {
                                if (!disabled && confirm.press('clearblocks', `Click again to unblock everybody: they can all connect ${word} again.`, event)) {
                                    card.act('clearblocks', () => adminClearAccountBlocks(service), 'The block list is empty.');
                                }
                            }}
                        >{'Clear the block list'}</AdminPill>
                    </div>
                </>
            ) : null}

            {quarantined ? (
                <div
                    className='vntv-admin-callout'
                    data-tone='warning'
                >
                    <p className='vntv-admin-line'>{SET_ASIDE[service]}</p>
                </div>
            ) : null}

            <p
                id={deleteAllId}
                className='vntv-admin-line vntv-admin-quiet vntv-twitch-deleteall'
            >{DELETE_ALL_HINT[service]}</p>
            <div className='vntv-admin-actions'>
                <AdminPill
                    tone='danger'
                    off={disabled}
                    waiting={waiting}
                    working={card.busy === 'deleteall'}
                    workingWords='Deleting…'
                    confirms={true}
                    armed={confirm.armed === 'deleteall'}
                    describedBy={deleteAllId}
                    onPress={(event) => {
                        if (!disabled && confirm.press('deleteall', `Click again to disconnect everybody and delete all ${word} data. This cannot be undone.`, event)) {
                            card.act('deleteall', () => deleteAllAccount(service), `Deleted all ${word} data. Everybody is disconnected; the block list is kept.`);
                        }
                    }}
                >{`Delete all ${word} data`}</AdminPill>
                <AdminPill
                    waiting={waiting}
                    onPress={card.load}
                >{'Refresh'}</AdminPill>
            </div>
            <p className='vntv-admin-line vntv-admin-quiet vntv-account-admin-after'>{DELETE_ALL_AFTER[service]}</p>
        </>
    );
};
