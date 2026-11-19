import React from 'react';
import {createPortal} from 'react-dom';

import {clapperBeside} from '../awardLogic';
import {clipErrorCopy, clipTime, goesWords, lengthInWords, orderClips, steadyOrder, streamLabel, tailLine} from '../clipLogic';
import {
    backToList,
    clipErrorCodeOf,
    clipErrorTextOf,
    clipsSeenBefore,
    closeClips,
    discardDraft,
    editDraft,
    getClip,
    loadClips,
    markClipsSeen,
    refreshDrafts,
    rememberSort,
    rememberedSort,
    setListFilter,
    showClip,
    startClip,
    takeDraftToRefocus,
    takeTailToRefocus,
    useClipList,
    useDrafts,
    useHeldTails,
} from '../clips';
import type {ClipFilter, ClipSort, DialogState} from '../clips';
import {catchStrayKey, keepKeysInside, trapTab} from '../focusTrap';
import {nameNow} from '../name';
import {useSnapshot} from '../store';
import {useTwoClicks} from '../twoClicks';
import type {Clip, Me, Snapshot} from '../types';

import {ClipCard} from './ClipCard';
import {ClipViewer} from './ClipViewer';
import {ClapperIcon, CloseIcon, ScissorsIcon} from './icons';

// The Clips dialog (spec 3.9, with the addendum's 3.8): the ends of streams
// that have just stopped, still held for clipping; the reader's drafts; every
// clip that has not expired; and one clip played. Opened from anywhere
// through clips.ts and drawn by ClipsRoot.
//
// For people who may watch clips: linked, or let in by an admin to watch only
// (design/lock-screen.md 5), who get the list and the viewer and nothing to
// act with — no Just ended, no drafts, no Yours, and in the viewer no upvote
// button, Report, Delete or Share. ClipsRoot draws nothing at all for anybody
// else; a clip asked for behind the lock opens once it is unlocked.
//
// A portal on document.body, like the Trophies card: the side panel clips
// anything opened in place, and this is opened from outside the panel too.

// Ids for the dialog's title, unique per opening, from a counter (see
// TitleSearch); and not vntv-, which the style check reads as a class name.
let dialogs = 0;

// The time, kept fresh enough for "2 min ago" and "goes in 52 min".
function useNow(ms: number): number {
    const [now, setNow] = React.useState(() => Date.now());

    React.useEffect(() => {
        const timer = window.setInterval(() => setNow(Date.now()), ms);

        return () => window.clearInterval(timer);
    }, [ms]);

    return now;
}

// Whether the component is still mounted, for answers that arrive later.
function useAlive(): React.MutableRefObject<boolean> {
    const alive = React.useRef(true);

    React.useEffect(() => {
        alive.current = true;

        return () => {
            alive.current = false;
        };
    }, []);

    return alive;
}

// Where focus goes when the button that had it went with its row: the first
// of these there is, in this order — the next thing down the dialog, or Close.
function focusFirstOf(selectors: string[]): void {
    const box = document.querySelector('.vntv-clips');
    for (const selector of selectors) {
        const target = box?.querySelector<HTMLElement>(selector);
        if (target) {
            target.focus({preventScroll: true});

            return;
        }
    }
}

// Streams that went off, or dropped and have not come back, whose last few
// minutes are still held (spec 3.9 item 1): the people who were watching can
// clip the end, which the server decides when the button is pressed. A
// stream that is live again is not here: its own tile offers the end before
// its drop. Each row goes when its end is let go, by this browser's clock;
// focus on a row that goes moves on down the dialog rather than to the page
// under it.
const JustEnded: React.FC<{snapshot: Snapshot; now: number; lengthSeconds: number}> = ({snapshot, now, lengthSeconds}) => {
    const live = snapshot.streams.map((stream) => stream.handle);
    const tails = useHeldTails(snapshot.clips?.enabled ? snapshot.clipTails : undefined, live);
    const hadFocus = React.useRef(false);
    const shown = tails.map((tail) => tail.handle).join('|');

    React.useLayoutEffect(() => {
        const active = document.activeElement;
        if (hadFocus.current && (!active || active === document.body)) {
            hadFocus.current = false;
            focusFirstOf(['.vntv-clips-tail button', '.vntv-clips-draft button', '.vntv-clips-head button', '.vntv-clipcard-play',
                '.vntv-clips-empty', '.vntv-modal-close']);
        }
    }, [shown]);

    if (tails.length === 0) {
        return null;
    }

    return (
        <section
            className='vntv-clips-ended'
            aria-label='Just ended'
            onFocus={() => {
                hadFocus.current = true;
            }}
            onBlur={(event) => {
                // Only a move somewhere else; a button taken out of the page
                // with focus on it moves focus nowhere, which is when it is
                // handed on.
                const next = event.relatedTarget as Node | null;
                if (next && !event.currentTarget.contains(next)) {
                    hadFocus.current = false;
                }
            }}
        >
            <div className='vntv-clips-heading-row'>
                <h3 className='vntv-clips-heading'>{'Just ended'}</h3>
            </div>
            <ul className='vntv-clips-rows'>
                {tails.map((tail) => {
                    const line = tailLine(tail, now, lengthSeconds);

                    return (
                        <li
                            key={tail.handle}
                            className='vntv-clips-tail'
                            data-handle={tail.handle}
                        >
                            <span className='vntv-clips-tail-text'>{line.text}</span>
                            <button
                                type='button'
                                className='vntv-clips-btn'
                                aria-label={line.label}
                                onClick={(event) => {
                                    hadFocus.current = false;
                                    startClip({handle: tail.handle, title: tail.title}, 'tail', lengthSeconds, event.currentTarget, tail.reason);
                                }}
                            >
                                <ScissorsIcon size={13}/>
                                {line.button}
                            </button>
                        </li>
                    );
                })}
            </ul>
        </section>
    );
};

// The reader's drafts: only they can see them, for two hours. Edit opens the
// editor on one, in the dialog's place (closing the editor comes back here);
// Discard takes two presses.
const Drafts: React.FC<{now: number}> = ({now}) => {
    const drafts = useDrafts().filter((draft) => draft.state !== 'published' && draft.expiresAt > now);
    const twice = useTwoClicks();
    const [busy, setBusy] = React.useState('');
    const [failure, setFailure] = React.useState('');
    const alive = useAlive();

    // A discarded draft takes its Discard button with it. Focus that was on
    // it goes to the Discard now in its place (or the one above, for the last
    // row), and with no drafts left to whatever comes next — never to the
    // page under the dialog, where the next letter typed would land in
    // Mattermost's message box.
    const handOn = (index: number) => {
        const active = document.activeElement;
        if (active && active !== document.body) {
            return;
        }

        const discards = Array.from(document.querySelectorAll<HTMLElement>('.vntv-clips .vntv-clips-draft [data-action="discard"]'));
        const next = discards[Math.min(index, discards.length - 1)];
        if (next) {
            next.focus({preventScroll: true});

            return;
        }

        focusFirstOf(['.vntv-clips-head button', '.vntv-clipcard-play', '.vntv-clips-empty', '.vntv-modal-close']);
    };

    if (drafts.length === 0) {
        return null;
    }

    return (
        <section
            className='vntv-clips-drafts'
            aria-label='Your drafts'
        >
            <div className='vntv-clips-heading-row'>
                <h3 className='vntv-clips-heading'>{'Your drafts'}</h3>
                <span className='vntv-clips-heading-note'>{'· only you can see these, kept for 2 hours'}</span>
            </div>
            <ul className='vntv-clips-rows'>
                {drafts.map((draft, index) => {
                    let discardWords = 'Discard';
                    if (busy === draft.id) {
                        discardWords = 'Discarding…';
                    } else if (twice.armed === draft.id) {
                        discardWords = 'Click again to discard';
                    }

                    return (
                        <li
                            key={draft.id}
                            className='vntv-clips-draft'
                            data-draft={draft.id}
                        >
                            <span className='vntv-clips-draft-text'>
                                {`${clipTime(draft.durationMs)} · ${streamLabel(draft.streamer)} · ${goesWords(draft.expiresAt, now)}`}
                            </span>
                            {draft.state === 'publishing' ? (
                                <span className='vntv-clips-draft-busy'>{'Publishing…'}</span>
                            ) : (
                                <span className='vntv-clips-draft-actions'>
                                    <button
                                        type='button'
                                        className='vntv-clips-btn'
                                        data-action='edit'
                                        aria-disabled={busy !== '' ? true : undefined}
                                        aria-label={`Edit the ${clipTime(draft.durationMs)} draft of ${streamLabel(draft.streamer)}`}
                                        onClick={() => {
                                            if (busy === '') {
                                                editDraft(draft);
                                            }
                                        }}
                                    >{'Edit'}</button>
                                    <button
                                        type='button'
                                        className='vntv-clips-btn'
                                        data-action='discard'
                                        aria-disabled={busy !== '' ? true : undefined}
                                        data-armed={twice.armed === draft.id ? 'true' : undefined}
                                        onClick={(event) => {
                                            if (busy !== '' || !twice.press(draft.id, event)) {
                                                return;
                                            }

                                            const hadFocus = document.activeElement === event.currentTarget;
                                            setBusy(draft.id);
                                            setFailure('');
                                            discardDraft(draft.id).then(() => {
                                                if (alive.current && hadFocus) {
                                                    handOn(index);
                                                }
                                            }, (reason: unknown) => {
                                                if (alive.current) {
                                                    setFailure(clipErrorCopy(clipErrorCodeOf(reason), 'draft', {brand: nameNow()}, clipErrorTextOf(reason)).text);
                                                }
                                            }).then(() => {
                                                if (alive.current) {
                                                    setBusy('');
                                                }
                                            });
                                        }}
                                    >{discardWords}</button>
                                </span>
                            )}
                        </li>
                    );
                })}
            </ul>
            <p
                className='vntv-clips-status vntv-notice-error'
                role='alert'
            >{failure}</p>
        </section>
    );
};

const plural = (count: number, one: string): string => `${count} ${one}${count === 1 ? '' : 's'}`;

type ListProps = {
    // Linked: everything. Not: let in to watch only.
    linked: boolean;
    filter: ClipFilter;
    snapshot: Snapshot;

    // The newest clip the reader had seen before this opening: 0 when they
    // never had, null when it cannot be known. Clips after it are new.
    seenBefore: number | null;
    now: number;
    onOpen: (id: string) => void;
};

const ListView: React.FC<ListProps> = ({linked, filter, snapshot, seenBefore, now, onOpen}) => {
    const list = useClipList();
    const [sort, setSort] = React.useState<ClipSort>(rememberedSort);
    const info = snapshot.clips;
    const lengthSeconds = info?.lengthSeconds || 120;
    const yours = linked && filter === 'yours';

    const all = list.ids.map(getClip).filter((clip): clip is Clip => Boolean(clip));
    const candidates = yours ? all.filter((clip) => clip.mine || clip.ofMyStream) : all;

    // Sorted when the list opens and whenever Top or New is pressed (again,
    // too), and otherwise kept as it is: an upvote, or a refetch it causes,
    // changes counts in place rather than moving cards about (steadyOrder).
    // Clips that arrive meanwhile take their sorted places.
    const [sorting, setSorting] = React.useState(0);
    const order = React.useRef<{key: string; ids: string[]}>({key: '', ids: []});
    const orderKey = `${sort}|${filter}|${sorting}`;
    const sortedNow = orderClips(candidates, sort).map((clip) => clip.id);
    const ids = order.current.key === orderKey ? steadyOrder(order.current.ids, sortedNow) : sortedNow;
    order.current = {key: orderKey, ids};
    const shown = ids.map(getClip).filter((clip): clip is Clip => Boolean(clip));

    // Nothing is new to somebody who has never looked: every clip would be.
    const fresh = (clip: Clip) => typeof seenBefore === 'number' && seenBefore > 0 && clip.createdAt > seenBefore;
    const freshCount = all.filter(fresh).length;
    const bestId = clapperBeside(snapshot);
    // Somebody live who can be clipped: never a 24/7 channel, and never a
    // Twitch stream, which is never recorded (design/twitch-live.md 9).
    const anybodyLive = snapshot.streams.some((stream) => !stream.alwaysOn && !stream.source);

    // Retry goes away as it is pressed, so focus that was on it waits on the
    // loading line and then goes to the first clip, or to whatever came back
    // instead — never to the page under the dialog.
    const refocus = React.useRef(false);
    React.useEffect(() => {
        if (!refocus.current) {
            return;
        }

        const box = document.querySelector('.vntv-clips');
        if (list.status === 'loading') {
            box?.querySelector<HTMLElement>('.vntv-clips-loading')?.focus();

            return;
        }

        refocus.current = false;
        box?.querySelector<HTMLElement>('.vntv-clipcard-play, .vntv-clips-empty .vntv-pill, .vntv-clips-empty')?.focus();
    }, [list.status]);

    // The line above the grid, and the order and filter beside it, only once
    // there are clips to count and order: "2 of 6 clips" while Yours is on.
    const hasClips = list.status === 'ready' && all.length > 0;
    let summary = '';
    if (hasClips) {
        const counted = yours ? `${shown.length} of ${plural(all.length, 'clip')}` : plural(all.length, 'clip');
        summary = `${counted} · kept for ${plural(info?.retentionDays || 30, 'day')}`;
        if (freshCount > 0) {
            summary += ` · ${freshCount} new`;
        }
    }

    let grid: React.ReactNode;
    if (list.status === 'error') {
        const copy = clipErrorCopy(list.error || 'network', 'list', {brand: nameNow()});
        grid = (
            <div
                className='vntv-clips-empty'
                role='alert'
                tabIndex={-1}
            >
                <p className='vntv-clips-empty-title'>{copy.text}</p>
                {copy.actions.includes('retry') && (
                    <button
                        type='button'
                        className='vntv-pill'
                        onClick={(event) => {
                            refocus.current = document.activeElement === event.currentTarget;
                            loadClips();
                        }}
                    >{'Retry'}</button>
                )}
            </div>
        );
    } else if (list.status !== 'ready') {
        grid = (
            <>
                <p
                    className='vntv-sr-only vntv-clips-loading'
                    role='status'
                    tabIndex={-1}
                >{'Loading clips…'}</p>
                <ul
                    className='vntv-clips-grid'
                    aria-hidden={true}
                >
                    {[0, 1, 2].map((index) => (
                        <li
                            key={index}
                            className='vntv-clips-ghost'
                        >
                            <span className='vntv-clips-ghost-pic'/>
                            <span className='vntv-clips-ghost-line'/>
                            <span className='vntv-clips-ghost-line'/>
                        </li>
                    ))}
                </ul>
            </>
        );
    } else if (shown.length === 0) {
        let title = 'No clips yet.';
        let hint = `Press ✂ on a stream to keep the last ${lengthInWords(lengthSeconds)} of it.`;
        if (!linked) {
            // Watch only: nothing invites them to make one.
            hint = '';
        } else if (yours && all.length > 0) {
            title = 'None of these are yours yet.';
            hint = 'Clips you make, and clips of your stream, are listed here.';
        } else if (info && !info.enabled) {
            hint = 'Clipping has been switched off.';
        } else if (!anybodyLive) {
            hint = 'Clips can be made while somebody is live.';
        }

        grid = (
            <div
                className='vntv-clips-empty'
                tabIndex={-1}
            >
                <span
                    className='vntv-clips-empty-icon'
                    aria-hidden={true}
                ><ClapperIcon size={30}/></span>
                <p className='vntv-clips-empty-title'>{title}</p>
                {hint !== '' ? <p className='vntv-clips-empty-hint'>{hint}</p> : null}
            </div>
        );
    } else {
        grid = (
            <ul
                className='vntv-clips-grid'
                aria-label={yours ? 'Your clips' : 'Clips'}
            >
                {shown.map((clip) => (
                    <ClipCard
                        key={clip.id}
                        clip={clip}
                        fresh={fresh(clip)}
                        best={Boolean(bestId) && clip.clipper.userId === bestId}
                        now={now}
                        onOpen={onOpen}
                    />
                ))}
            </ul>
        );
    }

    return (
        <>
            {linked ? (
                <>
                    <JustEnded
                        snapshot={snapshot}
                        now={now}
                        lengthSeconds={lengthSeconds}
                    />
                    <Drafts now={now}/>
                </>
            ) : null}

            {hasClips ? (
                <div className='vntv-clips-head'>
                    <p className='vntv-clips-summary'>{summary}</p>

                    {/* One item, so that a line too narrow for all three
                        puts both controls under the summary, not Yours alone. */}
                    <div className='vntv-clips-tools'>
                        <span
                            className='vntv-clips-sort'
                            role='group'
                            aria-label='Order'
                        >
                            {(['top', 'new'] as const).map((value) => (
                                <button
                                    key={value}
                                    type='button'
                                    className='vntv-clips-pill'
                                    aria-pressed={sort === value}
                                    title={value === 'top' ? 'Most upvoted first' : 'Newest first'}
                                    onClick={() => {
                                        setSort(value);
                                        setSorting((count) => count + 1);
                                        rememberSort(value);
                                    }}
                                >{value === 'top' ? 'Top' : 'New'}</button>
                            ))}
                        </span>
                        {linked ? (
                            <button
                                type='button'
                                className='vntv-clips-pill vntv-clips-filter'
                                aria-pressed={yours}
                                title='Only clips you made, and clips of your stream'
                                onClick={() => setListFilter(yours ? 'all' : 'yours')}
                            >{'Yours'}</button>
                        ) : null}
                    </div>
                </div>
            ) : null}

            {grid}
        </>
    );
};

type Props = {
    state: NonNullable<DialogState>;

    // Linked, or (false) let in to watch only.
    linked: boolean;
    me: Me;
};

export const ClipsModal: React.FC<Props> = ({state, linked, me}) => {
    const snapshot = useSnapshot();
    const now = useNow(10000);
    const card = React.useRef<HTMLDivElement>(null);
    const body = React.useRef<HTMLDivElement>(null);
    const closer = React.useRef<HTMLButtonElement>(null);
    const [ids] = React.useState(() => {
        dialogs += 1;

        return {title: `villanet-clips-title-${dialogs}`};
    });

    // What to show: one clip, or the list. A clip asked for behind the lock
    // is the clip, now that the dialog is drawn at all.
    let view: 'list' | 'clip';
    let clipId: string | undefined;
    if (state.view === 'clip') {
        clipId = state.id;
    } else if (state.view === 'locked') {
        clipId = state.clipId;
    }
    if (clipId) {
        view = 'clip';
    } else {
        view = 'list';
    }
    const direct = state.view === 'clip' ? state.direct : true;
    const filter: ClipFilter = (state.view === 'list' || state.view === 'clip') && state.filter ? state.filter : 'all';

    // What the reader had seen before this opening, for the NEW tags, read
    // once: the list marks everything seen as soon as it shows.
    const [seenBefore] = React.useState(clipsSeenBefore);
    const newest = snapshot.clips?.newest;
    React.useEffect(() => {
        if (view === 'list') {
            markClipsSeen(newest);
        }
    }, [view, newest]);

    // The list, fetched when the dialog opens and whenever any clip changes,
    // and again when the reader's rights change, so the flags on each clip
    // are theirs; the drafts only for somebody linked.
    const version = snapshot.clips?.version;
    React.useEffect(() => {
        loadClips();
    }, [linked, version]);
    React.useEffect(() => {
        if (linked) {
            refreshDrafts();
        }
    }, [linked]);

    // Going from the list to a clip and back keeps the list where it was and
    // puts focus back on the card that was opened.
    const listScroll = React.useRef(0);
    const returnTo = React.useRef<string | null>(null);
    const open = React.useCallback((id: string) => {
        listScroll.current = body.current?.scrollTop || 0;
        returnTo.current = id;
        showClip(id);
    }, []);

    // Escape: from a clip opened off the list, back to the list; otherwise
    // closed. Read through a ref, so the listener is added once.
    const escape = React.useRef<() => void>(closeClips);
    escape.current = view === 'clip' && !direct ? backToList : closeClips;

    // What had focus when the dialog opened — the clapper, the panel's row —
    // which gets it back when it closes. Read as the dialog first renders,
    // before the focus below moves into it, and given back only when focus
    // went with the dialog.
    const [opener] = React.useState(() => document.activeElement as HTMLElement | null);

    // Escape and Tab, on the document in the capture phase, as the Trophies
    // card does: stopping Escape there keeps Mattermost from closing whatever
    // is under the dialog, and it means no handler inside the dialog ever
    // sees Escape — which is why going back from a clip is here too. A video
    // in fullscreen keeps its Escape for leaving fullscreen. A letter typed
    // while focus is on nothing at all is kept from the message box here too.
    React.useEffect(() => {
        const onKey = (event: KeyboardEvent) => {
            if (event.key === 'Escape') {
                if (document.fullscreenElement) {
                    return;
                }

                event.stopPropagation();
                escape.current();

                return;
            }

            if (catchStrayKey(event, card.current)) {
                return;
            }

            trapTab(event, card.current);
        };

        document.addEventListener('keydown', onKey, true);

        return () => {
            document.removeEventListener('keydown', onKey, true);
            const active = document.activeElement;
            if (opener?.isConnected && (!active || active === document.body)) {
                opener.focus?.();
            }
        };
    }, [opener]);

    // Focus, whenever what is shown changes: a clip on Back, and the list on
    // the card it was left from, or else on Close. Before paint, with the
    // list's scroll put back first.
    const showing = view === 'clip' ? `clip:${clipId}` : view;
    React.useLayoutEffect(() => {
        const box = card.current;
        if (!box) {
            return;
        }

        let target: HTMLElement | null = null;
        if (view === 'clip') {
            if (body.current) {
                body.current.scrollTop = 0;
            }
            target = box.querySelector<HTMLElement>('.vntv-clipview-back');
        } else {
            if (body.current && returnTo.current) {
                body.current.scrollTop = listScroll.current;
            }
            if (returnTo.current) {
                target = box.querySelector<HTMLElement>(`[data-clip="${returnTo.current}"]`);
            }
            returnTo.current = null;

            // Back from the editor a draft's Edit opened: on that Edit. Back
            // from one Clip the end opened, with no draft made: on that row's
            // button again, while the end is still held.
            const draftId = takeDraftToRefocus();
            const tailHandle = takeTailToRefocus();
            const backTo = (draftId ? box.querySelector<HTMLElement>(`[data-draft="${draftId}"] [data-action="edit"]`) : null) ||
                (tailHandle ? box.querySelector<HTMLElement>(`.vntv-clips-tail[data-handle="${CSS.escape(tailHandle)}"] button`) : null);
            target = backTo || target;
        }

        (target || closer.current)?.focus({preventScroll: true});
    }, [showing]);

    let content: React.ReactNode;
    if (view === 'clip' && clipId) {
        content = (
            <ClipViewer
                key={clipId}
                id={clipId}
                me={me}
                bestClipperId={clapperBeside(snapshot)}
                version={version}
                now={now}
                onBack={backToList}
            />
        );
    } else {
        content = (
            <ListView
                linked={linked}
                filter={filter}
                snapshot={snapshot}
                seenBefore={seenBefore}
                now={now}
                onOpen={open}
            />
        );
    }

    // The card can take focus by script, never by Tab: it is where focus goes
    // when the button that had it is taken away (catchStrayKey), and where a
    // click on the card's background puts it, so keys pressed after either
    // still pass through keepKeysInside rather than reaching the message box.
    return createPortal(
        <div
            className='vntv vntv-modal vntv-clips-modal'
            role='presentation'
            onMouseDown={(event) => {
                // Only a press that both starts and ends on the backdrop.
                // Otherwise a text selection dragged out of the card closes it.
                if (event.target === event.currentTarget) {
                    closeClips();
                }
            }}
        >
            <div
                ref={card}
                className='vntv-modal-card vntv-clips'
                data-view={view}
                role='dialog'
                aria-modal={true}
                aria-labelledby={ids.title}
                tabIndex={-1}
                onMouseDown={(event) => event.stopPropagation()}
                onKeyDown={keepKeysInside}
            >
                <header className='vntv-modal-head'>
                    <span
                        className='vntv-modal-icon'
                        aria-hidden={true}
                    ><ClapperIcon size={20}/></span>
                    <h2
                        id={ids.title}
                        className='vntv-modal-title'
                    >{'Clips'}</h2>
                    <button
                        ref={closer}
                        type='button'
                        className='vntv-modal-close'
                        aria-label='Close'
                        onClick={closeClips}
                    ><CloseIcon size={16}/></button>
                </header>

                <div
                    ref={body}
                    className='vntv-modal-body vntv-clips-body'
                >{content}</div>
            </div>
        </div>,
        document.body,
    );
};
