import React from 'react';

import {adminBlockTwitch, adminClearTwitchBlocks, adminTwitchAllDay, adminUnlinkTwitch, deleteAllTwitch, fetchAdminTwitch} from '../client';
import {formatDuration} from '../format';
import {PRODUCT_NAME} from '../name';
import {channelURL} from '../twitchLogic';
import type {AdminTwitch as Status, AdminTwitchPerson} from '../types';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardConfirm, useCardFocus} from './adminParts';
import type {AdminCardProps} from './adminParts';
import {Avatar} from './Avatar';

// System Console > Plugins > Matty > Twitch > Twitch connections, the TwitchSetup
// setting (design/twitch-live.md 2.4): the exact OAuth Redirect URL to add
// to the Twitch application, whether the application and Twitch's player
// work here, when Twitch was last asked, who has connected — with
// Disconnect, Disconnect and block, and 24/7 for each — who is blocked, the
// Twitch hours kept, and a way to delete everything the plugin holds from
// Twitch.
//
// Everything here works whatever Show people live on Twitch says, so a
// connection can always be taken away: a person's right to withdraw, and an
// admin's to remove them, do not depend on the switch. Everything acts at
// once, like the Access card; nothing here is a setting or waits for the
// page's Save button. Admin routes only, so it works for an admin whose own
// panel is locked. Read when shown and again after each action, never
// polled. Twitch ids are never sent to it.
//
// Taking somebody's connection away, blocking them, clearing the block list
// and deleting everything each take a second press, as everything on the
// page that cannot be undone does.

function errorText(reason: unknown): string {
    return reason instanceof Error ? reason.message : String(reason);
}

function plural(count: number, one: string, many: string): string {
    return `${count} ${count === 1 ? one : many}`;
}

function day(at: number): string {
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
function ago(at: number, now: number): string {
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

const OFF_LINE = 'Show people live on Twitch is off: nobody can connect and nothing is asked of Twitch. Connected people below can still be disconnected.';
const ALL_DAY_LABEL = '24/7: tagged 24/7, no live badge, no airtime, like an always-on stream key.';
const DELETE_ALL_HINT = `Disconnects everybody and deletes every Twitch hour, name and id ${PRODUCT_NAME} holds, including set-aside copies. Broadcast Box hours are not touched.`;

// Ids from a counter (see TitleSearch). Not vntv-, which the style check
// reads as a class.
let nextCard = 0;

export const AdminTwitch: React.FC<AdminCardProps> = ({id, label, disabled}) => {
    const [status, setStatus] = React.useState<Status | null>(null);
    const [loadError, setLoadError] = React.useState('');
    const [busy, setBusy] = React.useState('');
    const [note, setNote] = React.useState('');
    const [error, setError] = React.useState('');
    const [now, setNow] = React.useState(() => Date.now());
    const {statusRef, focus} = useCardFocus();
    const confirm = useCardConfirm(() => {
        setNote('');
        setError('');
    });
    const urlRef = React.useRef<HTMLInputElement | null>(null);
    const [ids] = React.useState(() => {
        nextCard += 1;

        return {callback: `villanet-twitch-callback-${nextCard}`, deleteAll: `villanet-twitch-deleteall-${nextCard}`};
    });

    // Only the newest request's answer is used: the read after an action can
    // cross a Refresh, and an older answer landing last would bring back a
    // connection just taken away.
    const latest = React.useRef(0);
    const load = React.useCallback(() => {
        const ticket = ++latest.current;
        fetchAdminTwitch().then((next) => {
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
    }, []);

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

            // Either way it is read again: a failure is often because it had
            // already changed — somebody disconnected themselves meanwhile.
            load();
        });
    };

    // Copy: the address exactly, as Twitch matches it. Where the clipboard is
    // refused, the address is selected for Ctrl+C instead.
    const copy = () => {
        const url = status?.callbackUrl || '';
        if (url === '') {
            return;
        }

        const selectIt = () => {
            urlRef.current?.focus();
            urlRef.current?.select();
            setNote('Selected: press Ctrl+C (⌘C on a Mac) to copy it.');
        };

        if (!navigator.clipboard?.writeText) {
            selectIt();

            return;
        }

        navigator.clipboard.writeText(url).then(() => {
            setError('');
            setNote('Copied the OAuth Redirect URL.');
        }).catch(selectIt);
    };

    if (status === null) {
        return (
            <AdminCard
                id={id}
                label={label}
                focus={focus}
            >
                <AdminHead instant={true}>{loadError === '' ? 'Loading…' : 'Twitch connections could not be loaded.'}</AdminHead>
                {loadError === '' ? null : (
                    <div className='vntv-admin-actions'>
                        <AdminPill onPress={load}>{'Refresh'}</AdminPill>
                    </div>
                )}
                <AdminStatus
                    ref={statusRef}
                    note=''
                    error={loadError}
                />
            </AdminCard>
        );
    }

    const waiting = busy !== '';
    const people = status.people;
    const liveCount = people.filter((person) => person.live).length;
    const hours = status.hours;

    let head: React.ReactNode = 'Nobody has connected a Twitch account.';
    if (people.length > 0) {
        head = (
            <>
                <b>{plural(people.length, 'person', 'people')}</b>
                {` connected to Twitch${status.enabled && liveCount > 0 ? ` · ${liveCount} live now` : ''}`}
            </>
        );
    }

    // When Twitch was last asked, and what it said.
    const lastCheck = (() => {
        if (!status.enabled) {
            return {text: OFF_LINE, error: false};
        }

        // Switched on without an application: the server asks Twitch for
        // nothing until there is one, so "once a minute" would be untrue.
        if (!status.credentials) {
            return {text: 'Nothing is asked of Twitch until the Client ID and Client Secret are set. Connected people below can still be disconnected.', error: false};
        }
        if (status.problem) {
            return {text: status.problem, error: true};
        }
        if (!status.lastTry) {
            return {text: people.length === 0 ? 'Nothing to ask Twitch yet: nobody has connected.' : 'Not asked yet. Twitch is asked once a minute.', error: false};
        }

        return {text: `Asked Twitch ${ago(status.lastTry, now)}: ${liveCount} of ${plural(people.length, 'connected person', 'connected people')} live.`, error: false};
    })();

    const disconnectOf = (person: AdminTwitchPerson, block: boolean) => {
        const key = `${block ? 'block' : 'unlink'}:${person.userId}`;
        const words = block ?
            `Click again to disconnect ${person.name} from Twitch and stop them connecting again; their Twitch hours are deleted.` :
            `Click again to disconnect ${person.name} from Twitch; their Twitch hours are deleted. They can connect again.`;

        return (event: {detail: number}) => {
            if (disabled || !confirm.press(key, words, event, person.userId)) {
                return;
            }

            act(key, () => adminUnlinkTwitch(person.userId, block), block ?
                `Disconnected ${person.name} and blocked them from connecting Twitch.` :
                `Disconnected ${person.name} from Twitch.`);
        };
    };

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            <AdminHead instant={true}>{head}</AdminHead>

            <h4 className='vntv-admin-sub'>{'OAuth Redirect URL'}</h4>
            {status.callbackProblem ? (
                <p className='vntv-admin-line vntv-notice-error'>{status.callbackProblem}</p>
            ) : (
                <>
                    <div className='vntv-access-entry vntv-twitch-callback'>
                        <label
                            className='vntv-sr-only'
                            htmlFor={ids.callback}
                        >{'OAuth Redirect URL'}</label>
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
                            waiting={waiting}
                            label='Copy the OAuth Redirect URL'
                            onPress={copy}
                        >{'Copy'}</AdminPill>
                    </div>
                    <p className='vntv-admin-line vntv-admin-quiet'>
                        {'Add this exactly under OAuth Redirect URLs in your Twitch application (dev.twitch.tv/console/apps), then press Save there.'}
                    </p>
                </>
            )}

            <h4 className='vntv-admin-sub'>{'Application'}</h4>
            <p className='vntv-admin-line'>
                {status.credentials ? 'Client ID and Client Secret are set.' : 'Fill in the Client ID and Client Secret above first.'}
            </p>

            {status.embed ? (
                <>
                    <h4 className='vntv-admin-sub'>{'Twitch’s player at this address'}</h4>
                    {status.embedProblem ? (
                        <p className='vntv-admin-line vntv-notice-error'>{`${status.embedProblem} Twitch streams will open on twitch.tv instead.`}</p>
                    ) : (
                        <p className='vntv-admin-line'>{'Can run here.'}</p>
                    )}
                </>
            ) : null}

            <h4 className='vntv-admin-sub'>{'Last check'}</h4>
            <p className={lastCheck.error ? 'vntv-admin-line vntv-notice-error' : 'vntv-admin-line'}>{lastCheck.text}</p>

            <h4 className='vntv-admin-sub'>{'Connected people'}</h4>
            {people.length === 0 ? (
                <p className='vntv-admin-line vntv-admin-quiet'>{'Nobody has connected a Twitch account.'}</p>
            ) : (
                <ul className='vntv-admin-list'>
                    {people.map((person) => {
                        const out = channelURL(person.login);
                        const since = day(person.since);

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
                                        name={person.name}
                                        className='vntv-admin-thumb-image'
                                        decorative={true}
                                    />
                                </span>
                                <div className='vntv-admin-row-main'>
                                    <p className='vntv-admin-row-title'>
                                        <span className='vntv-admin-row-name'>{person.name}</span>
                                        {person.live && status.enabled ? (
                                            <span
                                                className='vntv-admin-flag'
                                                data-tone='danger'
                                            >{'Live'}</span>
                                        ) : null}
                                        {person.allDay ? <span className='vntv-admin-flag'>{'24/7'}</span> : null}
                                    </p>
                                    <p className='vntv-admin-row-meta'>
                                        {out !== '' ? (
                                            <a
                                                href={out}
                                                target='_blank'
                                                rel='noopener noreferrer'
                                            >{`twitch.tv/${person.login}`}</a>
                                        ) : person.login}
                                        {person.twitchName && person.twitchName.toLowerCase() !== person.login ? ` (${person.twitchName})` : ''}
                                        {since ? ` · connected ${since}` : ''}
                                    </p>
                                    <label className='vntv-admin-check vntv-twitch-allday'>
                                        <input
                                            type='checkbox'
                                            checked={person.allDay}
                                            disabled={disabled || waiting}
                                            onChange={(event) => {
                                                const allDay = event.target.checked;
                                                act(`allday:${person.userId}`, () => adminTwitchAllDay(person.userId, allDay), allDay ?
                                                    `${person.name}’s Twitch channel is 24/7 now: no live badge, no airtime.` :
                                                    `${person.name}’s Twitch channel is not 24/7 any more.`);
                                            }}
                                        />
                                        {ALL_DAY_LABEL}
                                    </label>
                                    <div
                                        className='vntv-admin-actions'
                                        role='group'
                                        aria-label={`${person.name}’s Twitch connection`}
                                    >
                                        <AdminPill
                                            off={disabled}
                                            waiting={waiting}
                                            working={busy === `unlink:${person.userId}`}
                                            workingWords='Disconnecting…'
                                            confirms={true}
                                            armed={confirm.armed === `unlink:${person.userId}`}
                                            label={`Disconnect ${person.name} from Twitch`}
                                            onPress={disconnectOf(person, false)}
                                        >{'Disconnect'}</AdminPill>
                                        <AdminPill
                                            off={disabled}
                                            waiting={waiting}
                                            working={busy === `block:${person.userId}`}
                                            workingWords='Blocking…'
                                            confirms={true}
                                            armed={confirm.armed === `block:${person.userId}`}
                                            label={`Disconnect ${person.name} from Twitch and block them`}
                                            onPress={disconnectOf(person, true)}
                                        >{'Disconnect and block'}</AdminPill>
                                    </div>
                                </div>
                            </li>
                        );
                    })}
                </ul>
            )}

            {status.blocked.length > 0 ? (
                <>
                    <h4 className='vntv-admin-sub'>{'Blocked'}</h4>
                    <p className='vntv-admin-line vntv-admin-quiet'>{'Blocked people cannot connect a Twitch account.'}</p>
                    <ul className='vntv-admin-list'>
                        {status.blocked.map((person) => (
                            <li
                                key={person.userId}
                                className='vntv-admin-row'
                            >
                                <span className='vntv-admin-row-text'>
                                    {person.since ? `${person.name} · since ${day(person.since)}` : person.name}
                                </span>
                                <AdminPill
                                    off={disabled}
                                    waiting={waiting}
                                    working={busy === `unblock:${person.userId}`}
                                    workingWords='Unblocking…'
                                    label={`Unblock ${person.name}`}
                                    onPress={() => act(`unblock:${person.userId}`, () => adminBlockTwitch(person.userId, false), `${person.name} can connect Twitch again.`)}
                                >{'Unblock'}</AdminPill>
                            </li>
                        ))}
                    </ul>
                    <div className='vntv-admin-actions'>
                        <AdminPill
                            off={disabled}
                            waiting={waiting}
                            working={busy === 'clearblocks'}
                            workingWords='Clearing…'
                            confirms={true}
                            armed={confirm.armed === 'clearblocks'}
                            onPress={(event) => {
                                if (!disabled && confirm.press('clearblocks', 'Click again to unblock everybody: they can all connect Twitch again.', event)) {
                                    act('clearblocks', adminClearTwitchBlocks, 'The block list is empty.');
                                }
                            }}
                        >{'Clear the block list'}</AdminPill>
                    </div>
                </>
            ) : null}

            <h4 className='vntv-admin-sub'>{'Twitch hours'}</h4>
            <p className='vntv-admin-line'>
                {hours.channels === 0 && hours.seconds === 0 ?
                    'No Twitch hours recorded.' :
                    `${formatDuration(hours.seconds)} recorded for ${plural(hours.channels, 'Twitch channel', 'Twitch channels')}.`}
            </p>
            {hours.purging > 0 ? (
                <p className='vntv-admin-line vntv-admin-quiet'>{`Removing the hours of ${plural(hours.purging, 'disconnected channel', 'disconnected channels')}.`}</p>
            ) : null}
            {hours.halted ? (
                <div
                    className='vntv-admin-callout'
                    data-tone='error'
                    role='alert'
                >
                    <p className='vntv-admin-line'>{hours.halted}</p>
                    <p className='vntv-admin-line'>{'Fix or delete the twitchair_… key, then disable and enable the plugin.'}</p>
                </div>
            ) : null}
            {hours.quarantined ? (
                <div
                    className='vntv-admin-callout'
                    data-tone='warning'
                >
                    <p className='vntv-admin-line'>
                        {'A set-aside copy of damaged Twitch data still exists and may hold disconnected people’s Twitch names and hours. Delete all Twitch data removes it.'}
                    </p>
                </div>
            ) : null}

            <p
                id={ids.deleteAll}
                className='vntv-admin-line vntv-admin-quiet vntv-twitch-deleteall'
            >{DELETE_ALL_HINT}</p>
            <div className='vntv-admin-actions'>
                <AdminPill
                    tone='danger'
                    off={disabled}
                    waiting={waiting}
                    working={busy === 'deleteall'}
                    workingWords='Deleting…'
                    confirms={true}
                    armed={confirm.armed === 'deleteall'}
                    describedBy={ids.deleteAll}
                    onPress={(event) => {
                        if (!disabled && confirm.press('deleteall', 'Click again to disconnect everybody and delete all Twitch data. This cannot be undone.', event)) {
                            act('deleteall', deleteAllTwitch, 'Deleted all Twitch data. Everybody is disconnected; the block list is kept.');
                        }
                    }}
                >{'Delete all Twitch data'}</AdminPill>
                <AdminPill
                    waiting={waiting}
                    onPress={load}
                >{'Refresh'}</AdminPill>
            </div>

            <AdminStatus
                ref={statusRef}
                note={confirm.hint || note}
                error={error || (loadError === '' ? '' : `Twitch connections could not be loaded: ${loadError}`)}
            />
        </AdminCard>
    );
};
