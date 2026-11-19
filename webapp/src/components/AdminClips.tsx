import React from 'react';

import {
    ClipRequestError,
    adminClipThumbURL,
    adminClipVideoURL,
    adminDeleteClip,
    adminWipeRecording,
    blockClipper,
    clearClipReports,
    fetchAdminClips,
    removeClipThumb,
    resetClipperScore,
    unblockClipper,
} from '../client';
import {
    adminClipLine,
    adminDeleteConfirm,
    adminPerson,
    adminPersonLine,
    adminSummary,
    heldTailLine,
    recorderLine,
    retentionLine,
} from '../clipLogic';
import type {AdminVotesLine} from '../clipLogic';
import {clipErrorTextOf, shownTitleOf} from '../clips';
import {useSnapshot} from '../store';
import {clockTime, dayWord, viewerZone} from '../time';
import type {AdminClips as ClipsOnServer, Clip} from '../types';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardConfirm, useCardFocus} from './adminParts';
import {ClipTile} from './ClipTile';
import {ClapperIcon, ThumbsUpIcon} from './icons';

// Clips, moderated from System Console (spec 3.14, with the addendum's 2.2
// and 3.3): every clip kept on this server, who made them and how much room
// they take, who is stopped from making them, and what is being recorded for
// clipping right now.
//
// For every system admin, whether or not they have linked a stream (the
// addendum's question A), and for one whose own panel is locked
// (design/lock-screen.md 8.2): it uses the console's own routes only, which
// the lock does not cover. The System Console renders none of the components the
// Clips dialog lives in, so a clip is watched here, in the row, from the
// console's own routes, in the same player as the dialog's (ClipTile.tsx,
// the stream tile's look) — and with no way to download it: Download is the
// panel's, for linked people (design/3.2.1.md), and is never passed here.
//
// Everything acts at once, like the preview pictures: nothing waits for the
// page's Save button, and none of it is a setting. Anything that cannot be
// undone takes a second click.

// How often the page looks again while it is open: recordings and votes
// change by the minute.
const REFRESH_MS = 20000;

type Props = {
    id?: string;
    label?: string;
    disabled?: boolean;
};

// The words the server refused with, which are fixed and plain; or, when it
// sent none — no answer at all, or a refusal from Mattermost or a proxy before
// the plugin saw the request — what is known.
function why(reason: unknown): string {
    const said = clipErrorTextOf(reason);
    if (said !== '') {
        return said;
    }

    const status = reason instanceof ClipRequestError ? reason.status : 0;

    return status === 0 ? 'The server did not answer just now.' : `The server refused it (HTTP ${status}).`;
}

// What the inline player says when the clip's file would not load: the list
// is looked at again — a clip that has gone leaves it, and its player with
// it — and the rest is worth trying again.
const VIDEO_FAILED = 'The clip could not be loaded just now.';

// When a lowered "Keep clips for" takes effect: "at 21:04 tomorrow".
function removalWhen(at: number | undefined, now: number): string {
    if (!at) {
        return '';
    }

    const zone = viewerZone();
    const day = dayWord(at, zone, now);
    const clock = clockTime(at, zone);
    if (day === 'Today') {
        return `at ${clock} today`;
    }
    if (day === 'Tomorrow') {
        return `at ${clock} tomorrow`;
    }

    return `at ${clock} on ${day}`;
}

export const AdminClips: React.FC<Props> = ({id, label, disabled}) => {
    const [view, setView] = React.useState<ClipsOnServer | null>(null);
    const [loadError, setLoadError] = React.useState('');
    const [busy, setBusy] = React.useState('');
    const [note, setNote] = React.useState('');
    const [error, setError] = React.useState('');

    // The row the last action was on, so its result is also shown beside it
    // (RowResult): the section is as long as the list of clips, and the
    // lines that announce results are at its top.
    const [actedOn, setActedOn] = React.useState('');
    const [now, setNow] = React.useState(() => Date.now());

    // A pill waiting for its second press says what that press will do,
    // beside its row, under the pill. Not in the lines at the top of the card,
    // as the other cards do, and without clearing what those lines say: any
    // change of height there would move the pill out from under the pointer
    // between the two clicks. A screen reader hears it from a line of its own.
    const confirm = useCardConfirm();

    // The clip length, for "too high a bitrate for the whole 2:00": the
    // snapshot the plugin keeps wherever Mattermost is open says it.
    const lengthSeconds = useSnapshot().clips?.lengthSeconds || 120;

    // The clip playing in its row, if any: one at a time.
    const [playing, setPlaying] = React.useState('');

    // A failed reload keeps what it had rather than emptying the page. Only
    // the newest request's answer is used: the timed refresh and the one
    // after an action can cross, and an older answer landing last would bring
    // back a clip that was just deleted.
    const latest = React.useRef(0);
    const load = React.useCallback(() => {
        const ticket = ++latest.current;
        fetchAdminClips().then((next) => {
            if (ticket !== latest.current) {
                return;
            }
            setView(next);
            setLoadError('');
            setNow(Date.now());
        }).catch((reason: unknown) => {
            if (ticket !== latest.current) {
                return;
            }
            setLoadError(why(reason));
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

    // A clip that has gone from the list stops playing with it.
    const clips = view?.clips || [];
    React.useEffect(() => {
        if (playing !== '' && !clips.some((clip) => clip.id === playing)) {
            setPlaying('');
        }
    }, [clips, playing]);

    const act = (id: string, row: string, work: () => Promise<unknown>, done: (answer: unknown) => string) => {
        setBusy(id);
        setNote('');
        setError('');
        setActedOn(row);
        confirm.disarm();

        work().then((answer) => {
            setNote(done(answer));
        }).catch((reason: unknown) => {
            setError(why(reason));
        }).finally(() => {
            setBusy('');

            // Either way the page is looked at again: a failure is often
            // because it had already changed — a clip already gone.
            load();
        });
    };

    // A pill that acts on its second press (twoClicks.ts): true on the press
    // that should act. The sentence is what that press will do.
    const confirmed = (what: string, row: string, hint: string, event: React.MouseEvent) => busy === '' && confirm.press(what, hint, event, row);

    // Every pill stays focusable while the card works (aria-disabled rather
    // than disabled), so a keyboard admin keeps their place in a list that can
    // run to hundreds of rows. A row that goes — a clip deleted, a held end
    // wiped, somebody unblocked — takes its pill with it, and focus then goes
    // to the line that says what was done, rather than to the top of the page
    // (focusHome.ts).
    const {statusRef, focus} = useCardFocus();

    if (view === null) {
        return (
            <AdminCard
                id={id}
                label={label}
                focus={focus}
            >
                <AdminHead instant={true}>{loadError === '' ? 'Loading…' : 'Clips could not be loaded.'}</AdminHead>
                <AdminStatus
                    ref={statusRef}
                    note=''
                    error={loadError}
                />
            </AdminCard>
        );
    }

    const waiting = busy !== '';
    const blockedIds = new Set(view.blocked.map((person) => person.userId));
    const oldest = clips.reduce((soonest, clip) => (soonest === 0 || clip.expiresAt < soonest ? clip.expiresAt : soonest), 0);
    const best = view.bestClipper;
    const people = view.people.slice().sort((a, b) => (b.bytes - a.bytes) || (b.upvotes - a.upvotes));

    // What is being recorded, and the ends held after streams stopped: empty
    // lists from a server that records nothing yet, and taken as empty if one
    // is ever left out.
    const recorders = view.recorders || [];
    const tails = view.tails || [];

    // Wipe, on a recording's row or on the held end's: either one wipes all
    // that is held of that stream (the recording so far and the end of an
    // earlier stretch alike), and every draft cut from it. Each row's button
    // is its own, so pressing one does not ask for the second press on the
    // other, and its result is said beside the row it was pressed on.
    const wipeButton = (row: 'recording' | 'tail', handle: string, title: string) => {
        const what = `wipe:${row}:${handle}`;
        const where = `${row}:${handle}`;

        return (
            <AdminPill
                off={disabled}
                waiting={waiting}
                working={busy === what}
                workingWords='Wiping…'
                confirms={true}
                armed={confirm.armed === what}
                label={`Wipe what is held of ${title}`}
                onPress={(event) => {
                    if (!confirmed(what, where, `Click again to wipe what is held of ${title}, and every draft cut from it.`, event)) {
                        return;
                    }
                    act(what, where, () => adminWipeRecording(handle), (answer) => {
                        const removed = (answer as {draftsRemoved?: number})?.draftsRemoved || 0;

                        return `Wiped what was held of ${title}.${removed > 0 ? ` ${removed === 1 ? '1 draft' : `${removed} drafts`} made from it ${removed === 1 ? 'is' : 'are'} gone.` : ''}`;
                    });
                }}
            >{'Wipe'}</AdminPill>
        );
    };

    // The last result beside the row it was on, and what a waiting second
    // press will do beside the row it is in.
    const echo = {note, error, actedOn, hint: confirm.hint, hintRow: confirm.row};

    // A plain row: what it is, its one button, and the last result on it.
    const plainRow = (key: string, row: string, text: React.ReactNode, button: React.ReactNode, state?: string) => (
        <li
            key={key}
            className='vntv-admin-row'
            data-state={state}
        >
            <span className='vntv-admin-row-text'>{text}</span>
            {button}
            <RowResult
                row={row}
                echo={echo}
            />
        </li>
    );

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            {/* What is wrong, first. */}
            {view.problem ? (
                <div
                    className='vntv-admin-callout'
                    data-tone='error'
                    role='alert'
                >
                    <p className='vntv-admin-line'>{`Clips are not working: ${view.problem}`}</p>
                </div>
            ) : null}
            {view.halted ? (
                <div
                    className='vntv-admin-callout'
                    data-tone='error'
                    role='alert'
                >
                    <p className='vntv-admin-line'>
                        {`Clips have stopped saving, so the stored list is not overwritten: ${view.halted} `}
                        {'Put the plugin’s clips record right, or delete it, then disable and enable the plugin.'}
                    </p>
                </div>
            ) : null}

            <AdminHead instant={true}>
                {adminSummary({
                    count: clips.length,
                    usedBytes: view.usedBytes,
                    limitBytes: view.limitBytes,
                    shareBytes: view.shareBytes,
                    freeBytes: view.freeBytes,
                    totalBytes: view.totalBytes,
                    ringBytes: view.ringBytes,
                    oldestGoesAt: oldest,
                    best: best ? {name: best.displayName || (best.username ? `@${best.username}` : best.userId), upvotes: best.upvotes} : null,
                }, now)}
            </AdminHead>
            <p className='vntv-admin-line vntv-admin-quiet'>
                {'For any system admin, whether or not they have linked a stream.'}
                {view.root ? (
                    <>
                        {' Stored in '}
                        <code className='vntv-admin-code'>{view.root}</code>
                    </>
                ) : null}
            </p>

            {view.retention.pendingRemovals > 0 ? (
                <div
                    className='vntv-admin-callout'
                    data-tone='warning'
                >
                    <p className='vntv-admin-line'>
                        {retentionLine(view.retention.days, view.retention.pendingRemovals, removalWhen(view.retention.removalAt, now))}
                    </p>
                </div>
            ) : null}

            {/* Always there, so what is put in them is announced: the result
                of what was just done, and anything that went wrong, the list
                failing to load included. Up here, under what the card is
                about, rather than under a list that can run to hundreds of
                rows; the row acted on shows it too. The status line is also
                where focus goes when the Wipe that had it goes away. */}
            <AdminStatus
                ref={statusRef}
                note={note}
                error={error || (loadError === '' ? '' : `Clips could not be loaded: ${loadError}`)}
            />
            <p
                className='vntv-sr-only'
                role='status'
            >{confirm.hint}</p>

            <h4 className='vntv-admin-sub'>{'Recording now'}</h4>
            {recorders.length === 0 ? (
                <p className='vntv-admin-line vntv-admin-quiet'>{'Nothing is being recorded right now.'}</p>
            ) : (
                <ul className='vntv-admin-list'>
                    {recorders.map((recorder) => plainRow(
                        recorder.handle,
                        `recording:${recorder.handle}`,
                        recorderLine(recorder, lengthSeconds),
                        wipeButton('recording', recorder.handle, recorder.title || 'that stream'),
                        recorder.state,
                    ))}
                </ul>
            )}

            {tails.length > 0 ? (
                <>
                    <h4 className='vntv-admin-sub'>{'Held after the end'}</h4>
                    <ul className='vntv-admin-list'>
                        {tails.map((tail) => plainRow(
                            tail.handle,
                            `tail:${tail.handle}`,
                            heldTailLine(tail, now, clockTime(tail.until, viewerZone())),
                            wipeButton('tail', tail.handle, tail.title || 'that stream'),
                        ))}
                    </ul>
                </>
            ) : null}

            <h4 className='vntv-admin-sub'>{'Clips'}</h4>
            {clips.length === 0 ? (
                <p className='vntv-admin-line vntv-admin-quiet'>{'No clips are kept right now.'}</p>
            ) : (
                <ul className='vntv-admin-list'>
                    {clips.map((clip) => (
                        <ClipRow
                            key={clip.id}
                            clip={clip}
                            now={now}
                            off={Boolean(disabled)}
                            busy={busy}
                            armed={confirm.armed}
                            blocked={Boolean(clip.clipper.userId && blockedIds.has(clip.clipper.userId))}
                            playing={playing === clip.id}
                            onPlay={() => setPlaying(playing === clip.id ? '' : clip.id)}
                            onVideoFailed={load}
                            confirmed={confirmed}
                            act={act}
                            echo={echo}
                        />
                    ))}
                </ul>
            )}

            <h4 className='vntv-admin-sub'>{'People'}</h4>
            {people.length === 0 ? (
                <p className='vntv-admin-line vntv-admin-quiet'>{'Nobody has a clip or an upvote yet.'}</p>
            ) : (
                <ul className='vntv-admin-list'>
                    {people.map((person) => {
                        const what = `reset:${person.userId}`;
                        const where = `person:${person.userId}`;
                        const named = adminPerson(person);

                        return plainRow(
                            person.userId,
                            where,
                            <AdminVotes line={adminPersonLine(person, view.shareBytes)}/>,
                            <AdminPill
                                off={disabled}
                                waiting={waiting}
                                working={busy === what}
                                workingWords='Resetting…'
                                confirms={true}
                                armed={confirm.armed === what}
                                label={`Reset ${named}’s score`}
                                onPress={(event) => {
                                    if (!confirmed(what, where, `Click again to reset ${named}’s score: their upvotes so far stop counting for Best Clipper.`, event)) {
                                        return;
                                    }
                                    act(what, where, () => resetClipperScore(person.userId), () => `${named} starts again from nothing for Best Clipper.`);
                                }}
                            >{'Reset score'}</AdminPill>,
                        );
                    })}
                </ul>
            )}

            <h4 className='vntv-admin-sub'>{'Blocked from clipping'}</h4>
            {view.blocked.length === 0 ? (
                <p className='vntv-admin-line vntv-admin-quiet'>{'Nobody is blocked.'}</p>
            ) : (
                <ul className='vntv-admin-list'>
                    {view.blocked.map((person) => {
                        const what = `unblock:${person.userId}`;
                        const named = adminPerson(person);

                        return plainRow(
                            person.userId,
                            `blocked:${person.userId}`,
                            `${named} · since ${new Date(person.blockedAt).toLocaleDateString()}`,
                            <AdminPill
                                off={disabled}
                                waiting={waiting}
                                working={busy === what}
                                workingWords='Unblocking…'
                                label={`${busy === what ? 'Unblocking' : 'Unblock'} ${named}`}
                                onPress={() => act(what, `blocked:${person.userId}`, () => unblockClipper(person.userId), () => `${named} can make clips again.`)}
                            >{'Unblock'}</AdminPill>,
                        );
                    })}
                </ul>
            )}

            <div className='vntv-admin-actions'>
                <AdminPill
                    waiting={waiting}
                    onPress={load}
                >{'Refresh'}</AdminPill>
            </div>
        </AdminCard>
    );
};

type Echo = {note: string; error: string; actedOn: string; hint: string; hintRow: string};

// The result of the last action, again beside the row it was on, for the eye:
// the lines at the top have already said it to a screen reader, so this is
// hidden from one. Or, in the row whose pill waits for its second press, what
// that press will do. Under the row's pills, so neither moves them. Nothing on
// any other row, or when there is nothing to say.
const RowResult: React.FC<{row: string; echo: Echo}> = ({row, echo}) => {
    if (echo.hint !== '' && echo.hintRow === row) {
        return (
            <p
                className='vntv-admin-row-result'
                aria-hidden={true}
            >{echo.hint}</p>
        );
    }

    const text = echo.actedOn === row ? echo.error || echo.note : '';
    if (text === '') {
        return null;
    }

    const failed = echo.error !== '';

    return (
        <p
            className={`vntv-admin-row-result${failed ? ' vntv-notice-error' : ''}`}
            aria-hidden={true}
        >{text}</p>
    );
};

type RowProps = {
    clip: Clip;
    now: number;
    off: boolean;
    busy: string;
    armed: string;
    blocked: boolean;
    playing: boolean;
    onPlay: () => void;

    // The inline player's file would not load: the list is looked at again.
    onVideoFailed: () => void;
    confirmed: (what: string, row: string, hint: string, event: React.MouseEvent) => boolean;
    act: (id: string, row: string, work: () => Promise<unknown>, done: (answer: unknown) => string) => void;
    echo: Echo;
};

// One clip: its picture, its title, who made it from whose stream and what it
// has earned, and what can be done about it — and, when Watch is pressed,
// the clip itself under it.
const ClipRow: React.FC<RowProps> = ({clip, now, off, busy, armed, blocked, playing, onPlay, onVideoFailed, confirmed, act, echo}) => {
    const shown = shownTitleOf(clip);
    const quoted = clip.title !== '' ? `“${clip.title}”` : shown;
    const reports = clip.reports || 0;
    const clipper = clip.clipper;
    const handle = clipper.username ? `@${clipper.username}` : clipper.name;
    const ids = {
        picture: `picture:${clip.id}`,
        remove: `delete:${clip.id}`,
        block: `block:${clipper.userId || ''}:${clip.id}`,
        clear: `clear:${clip.id}`,
    };
    const thumb = clip.thumb ? adminClipThumbURL(clip.id, clip.thumb) : '';
    const row = `clip:${clip.id}`;
    const video = adminClipVideoURL(clip.id);
    const explain = React.useCallback(() => {
        onVideoFailed();

        return Promise.resolve(VIDEO_FAILED);
    }, [onVideoFailed]);

    const waiting = busy !== '';

    return (
        <li
            className='vntv-admin-row'
            data-align='top'
            data-reported={reports > 0 ? 'true' : undefined}
        >
            <span
                className='vntv-admin-thumb'
                data-shape='wide'
                data-dark='true'
                data-fit='contain'
            >
                <AdminPicture
                    src={thumb}
                    alt={`The picture of ${quoted}`}
                />
            </span>

            <div className='vntv-admin-row-main'>
                <p className='vntv-admin-row-title'>
                    <span
                        className='vntv-admin-row-name'
                        title={shown}
                    >{shown}</span>
                    {reports > 0 ? (
                        <span
                            className='vntv-admin-flag'
                            data-tone='danger'
                        >{'Reported'}</span>
                    ) : null}
                </p>
                <p className='vntv-admin-row-meta'><AdminVotes line={adminClipLine(clip, now)}/></p>
                {clip.streamNote ? <p className='vntv-admin-row-meta'>{clip.streamNote}</p> : null}

                <div
                    className='vntv-admin-actions'
                    role='group'
                    aria-label={quoted}
                >
                    <AdminPill
                        pressed={playing}
                        label={`Watch ${quoted}`}
                        onPress={onPlay}
                    >{'Watch'}</AdminPill>

                    {reports > 0 ? (
                        <AdminPill
                            off={off}
                            waiting={waiting}
                            working={busy === ids.clear}
                            workingWords='Clearing…'
                            confirms={true}
                            armed={armed === ids.clear}
                            onPress={(event) => {
                                if (confirmed(ids.clear, row, `Click again to clear the ${reports === 1 ? 'report' : `${reports} reports`} on ${quoted} and keep it.`, event)) {
                                    act(ids.clear, row, () => clearClipReports(clip.id), () => `The reports on ${quoted} are cleared.`);
                                }
                            }}
                        >{'Clear reports'}</AdminPill>
                    ) : null}

                    {clip.thumb ? (
                        <AdminPill
                            off={off}
                            waiting={waiting}
                            working={busy === ids.picture}
                            workingWords='Removing…'
                            confirms={true}
                            armed={armed === ids.picture}
                            onPress={(event) => {
                                if (confirmed(ids.picture, row, `Click again to take down the picture of ${quoted}; its card shows the streamer’s picture instead.`, event)) {
                                    act(ids.picture, row, () => removeClipThumb(clip.id), () => `Took down the picture of ${quoted}; its card shows the streamer’s picture now.`);
                                }
                            }}
                        >{'Remove picture'}</AdminPill>
                    ) : null}

                    <AdminPill
                        off={off}
                        waiting={waiting}
                        working={busy === ids.remove}
                        workingWords='Deleting…'
                        confirms={true}
                        armed={armed === ids.remove}
                        onPress={(event) => {
                            if (confirmed(ids.remove, row, adminDeleteConfirm(quoted, clip.upvotes), event)) {
                                act(ids.remove, row, () => adminDeleteClip(clip.id), () => `Deleted ${quoted}.${clip.upvotes > 0 ? ' Its upvotes no longer count.' : ''}`);
                            }
                        }}
                    >{'Delete'}</AdminPill>

                    {clipper.userId && !blocked ? (
                        <AdminPill
                            off={off}
                            waiting={waiting}
                            working={busy === ids.block}
                            workingWords='Blocking…'
                            confirms={true}
                            armed={armed === ids.block}
                            onPress={(event) => {
                                if (!confirmed(ids.block, row, `Click again to block ${handle} from clipping: their drafts are thrown away and they cannot make more. Their clips stay.`, event)) {
                                    return;
                                }
                                const userId = clipper.userId || '';
                                act(ids.block, row, () => blockClipper(userId), (answer) => {
                                    const removed = (answer as {draftsRemoved?: number})?.draftsRemoved || 0;

                                    return `Blocked ${handle} from clipping.${removed > 0 ? ` ${removed === 1 ? 'Their draft was' : `${removed} of their drafts were`} discarded.` : ''} Their clips stay until they are deleted.`;
                                });
                            }}
                        >{`Block ${handle}`}</AdminPill>
                    ) : null}
                </div>
            </div>

            <RowResult
                row={row}
                echo={echo}
            />

            {playing ? (
                <div className='vntv-admin-clips-player'>
                    <ClipTile
                        key={video}
                        clip={clip}
                        src={video}
                        poster={thumb}
                        noPictureInPicture={true}
                        explain={explain}
                    />
                </div>
            ) : null}
        </li>
    );
};

// A clip's picture as listed. It can have been taken down by the time it is
// drawn; then the row shows the clapper, as for a clip with none.
const AdminPicture: React.FC<{src: string; alt: string}> = ({src, alt}) => {
    const [failed, setFailed] = React.useState(false);

    React.useEffect(() => setFailed(false), [src]);

    if (src === '' || failed) {
        return (
            <span
                className='vntv-admin-thumb-none'
                aria-hidden={true}
            ><ClapperIcon size={20}/></span>
        );
    }

    return (
        <img
            className='vntv-admin-thumb-image'
            src={src}
            alt={alt}
            loading='lazy'
            draggable={false}
            onError={() => setFailed(true)}
        />
    );
};

// A console line with its upvotes in it, drawn as a thumbs up and the number,
// as a clip's card draws them. The word is there for a screen reader, which
// would otherwise hear a bare number in the middle of the line.
//
// Where the line may wrap: after a dot, never before one (a no-break space
// ties each dot to the words before it); the last part before the votes
// ("399 KB", "448 KB of 4 GB") holds together with its dot; and the votes keep
// what follows them ("(12 banked)", "· 2 reports") with them. So no line
// starts with a dot, and no size or count is split in two.
function votesLead(before: string): string {
    const at = before.lastIndexOf(' · ');
    const head = at < 0 ? '' : before.slice(0, at + 3);
    const last = at < 0 ? before : before.slice(at + 3).replace(/ /g, '\u00a0');

    return `${head.replace(/ · /g, '\u00a0· ')}${last}\u00a0· `;
}

const AdminVotes: React.FC<{line: AdminVotesLine}> = ({line}) => (
    <>
        {votesLead(line.before)}
        <span className='vntv-admin-votes'>
            <ThumbsUpIcon size={13}/>
            {String(line.upvotes)}
            <span className='vntv-sr-only'>{line.upvotes === 1 ? ' upvote' : ' upvotes'}</span>
            {line.after}
        </span>
    </>
);
