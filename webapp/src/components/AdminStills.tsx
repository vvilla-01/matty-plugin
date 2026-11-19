import React from 'react';

import {blockSharer, fetchAdminStills, removeStill, stillURL, unblockSharer} from '../client';
import type {AdminStills as Stills} from '../client';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardConfirm, useCardFocus} from './adminParts';

// The preview pictures on the Live now cards, moderated from System Console.
//
// A preview is whatever the browser that took it sent: the plugin cannot tell
// a frame of the stream from any other picture. So this lists every one that
// is showing, with who took it, and gives an admin two things to do about one
// that should not be there — take it down, which the next real frame replaces,
// or block whoever took it, which takes down everything of theirs and stops
// them sharing any more. Blocks last until they are lifted, restarts included.
//
// Everything here acts at once, like the artwork: nothing waits for the
// page's Save button, and none of it is a setting.

// How often the list refreshes itself while the page is open: previews change
// every minute or two.
const REFRESH_MS = 20000;

// A block is a second click away, for a moment, so a stray one cannot shut
// somebody out (twoClicks.ts, through useCardConfirm).

type Props = {
    id?: string;
    label?: string;
    disabled?: boolean;
};

function ago(iso: string, now: number): string {
    const then = Date.parse(iso);
    if (Number.isNaN(then)) {
        return '';
    }

    const minutes = Math.round((now - then) / 60000);
    if (minutes < 1) {
        return 'just now';
    }
    if (minutes < 60) {
        return `${minutes} min ago`;
    }

    const hours = Math.round(minutes / 60);

    return `${hours} hour${hours === 1 ? '' : 's'} ago`;
}

function who(displayName: string, username: string, userId: string): string {
    if (displayName && username) {
        return `${displayName} (@${username})`;
    }

    return displayName || (username ? `@${username}` : userId);
}

export const AdminStills: React.FC<Props> = ({id, label, disabled}) => {
    const [list, setList] = React.useState<Stills | null>(null);
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

    // A failed reload keeps what it had rather than emptying the list. Only
    // the newest request's answer is used: the timed refresh and the one after
    // an action can cross, and an older answer landing last would bring back
    // a picture that was just taken down.
    const latest = React.useRef(0);
    const load = React.useCallback(() => {
        const ticket = ++latest.current;
        fetchAdminStills().then((next) => {
            if (ticket !== latest.current) {
                return;
            }
            setList(next);
            setLoadError('');
            setNow(Date.now());
        }).catch((reason: unknown) => {
            if (ticket !== latest.current) {
                return;
            }
            setLoadError(reason instanceof Error ? reason.message : String(reason));
        });
    }, []);

    React.useEffect(() => {
        load();
        const timer = window.setInterval(load, REFRESH_MS);

        return () => {
            window.clearInterval(timer);

            // Anything still on its way arrives to nobody.
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
            setError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => {
            setBusy('');

            // Either way the list is looked at again: a failure is often
            // because it had already changed — a picture already replaced.
            load();
        });
    };

    if (list === null) {
        return (
            <AdminCard
                id={id}
                label={label}
                focus={focus}
            >
                <AdminHead instant={true}>{loadError === '' ? 'Loading…' : 'The preview pictures could not be loaded.'}</AdminHead>
                <AdminStatus
                    ref={statusRef}
                    note=''
                    error={loadError}
                />
            </AdminCard>
        );
    }

    const waiting = busy !== '';
    const blockedIds = new Set(list.blocked.map((person) => person.userId));
    const showing = list.stills.length;

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            <AdminHead instant={true}>
                {showing === 0 ? 'No preview pictures are showing right now.' : (
                    <><b>{`${showing} preview picture${showing === 1 ? '' : 's'}`}</b>{' showing on Live now cards'}</>
                )}
            </AdminHead>

            {showing > 0 ? (
                <ul className='vntv-admin-list'>
                    {list.stills.map((still) => {
                        const person = who(still.byDisplayName, still.byUsername, still.byUserId);
                        const blockId = `block:${still.byUserId}`;
                        const isBlocked = blockedIds.has(still.byUserId);
                        const handle = still.byUsername ? `@${still.byUsername}` : person;

                        return (
                            <li
                                key={still.handle}
                                className='vntv-admin-row'
                                data-align='top'
                            >
                                <span
                                    className='vntv-admin-thumb'
                                    data-shape='wide'
                                >
                                    <StillPreview
                                        src={stillURL(still.handle, still.version)}
                                        alt={`The preview picture on ${still.title}’s card`}
                                        onGone={load}
                                    />
                                </span>

                                <div className='vntv-admin-row-main'>
                                    <p className='vntv-admin-row-title'>
                                        <span className='vntv-admin-row-name'>{still.title}</span>
                                    </p>
                                    <p className='vntv-admin-row-meta'>{`Taken by ${person} · ${ago(still.at, now)}`}</p>
                                    <div
                                        className='vntv-admin-actions'
                                        role='group'
                                        aria-label={`The preview picture on ${still.title}’s card`}
                                    >
                                        <AdminPill
                                            off={disabled}
                                            waiting={waiting}
                                            working={busy === `remove:${still.handle}`}
                                            workingWords='Removing…'
                                            onPress={() => act(`remove:${still.handle}`, () => removeStill(still.handle), `Took down the picture on ${still.title}’s card.`)}
                                        >{'Remove'}</AdminPill>

                                        {still.byUserId && !isBlocked ? (
                                            <AdminPill
                                                off={disabled}
                                                waiting={waiting}
                                                working={busy === blockId}
                                                workingWords='Blocking…'
                                                confirms={true}
                                                armed={confirm.armed === blockId}
                                                onPress={(event) => {
                                                    if (confirm.press(blockId, `Click again to block ${handle}: everything they have shared comes down, and they cannot share more until you unblock them.`, event)) {
                                                        act(blockId, () => blockSharer(still.byUserId), `Blocked ${person} from sharing preview pictures.`);
                                                    }
                                                }}
                                            >{`Block ${handle}`}</AdminPill>
                                        ) : null}
                                    </div>
                                </div>
                            </li>
                        );
                    })}
                </ul>
            ) : null}

            <h4 className='vntv-admin-sub'>{'Blocked from sharing'}</h4>
            {list.blocked.length === 0 ? (
                <p className='vntv-admin-line vntv-admin-quiet'>{'Nobody is blocked.'}</p>
            ) : (
                <ul className='vntv-admin-list'>
                    {list.blocked.map((person) => (
                        <li
                            key={person.userId}
                            className='vntv-admin-row'
                        >
                            <span className='vntv-admin-row-text'>
                                {`${who(person.displayName, person.username, person.userId)} · since ${new Date(person.blockedAt).toLocaleDateString()}`}
                            </span>
                            <AdminPill
                                off={disabled}
                                waiting={waiting}
                                working={busy === `unblock:${person.userId}`}
                                workingWords='Unblocking…'
                                label={`${busy === `unblock:${person.userId}` ? 'Unblocking' : 'Unblock'} ${who(person.displayName, person.username, person.userId)}`}
                                onPress={() => act(`unblock:${person.userId}`, () => unblockSharer(person.userId), `${who(person.displayName, person.username, person.userId)} can share preview pictures again.`)}
                            >{'Unblock'}</AdminPill>
                        </li>
                    ))}
                </ul>
            )}

            <div className='vntv-admin-actions'>
                <AdminPill
                    waiting={waiting}
                    onPress={load}
                >{'Refresh'}</AdminPill>
            </div>

            <AdminStatus
                ref={statusRef}
                note={confirm.hint || note}
                error={error || (loadError === '' ? '' : `The preview pictures could not be loaded: ${loadError}`)}
            />
        </AdminCard>
    );
};

// A picture as listed. It can have been taken down or replaced by the time it
// is drawn; then it says so and has the list looked at again, rather than
// showing a broken image.
const StillPreview: React.FC<{src: string; alt: string; onGone: () => void}> = ({src, alt, onGone}) => {
    const [gone, setGone] = React.useState(false);

    React.useEffect(() => setGone(false), [src]);

    if (gone) {
        return <span className='vntv-admin-row-meta'>{'Gone'}</span>;
    }

    return (
        <img
            className='vntv-admin-thumb-image'
            src={src}
            alt={alt}
            onError={() => {
                setGone(true);
                onGone();
            }}
        />
    );
};
